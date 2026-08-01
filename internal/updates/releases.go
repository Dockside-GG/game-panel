package updates

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
)

const (
	defaultReleasesURL = "https://api.github.com/repos/Dockside-GG/game-panel/releases?per_page=30"
	repositoryURL      = "https://github.com/Dockside-GG/game-panel"
)

var semverPattern = regexp.MustCompile(`^(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)(?:-([0-9A-Za-z.-]+))?(?:\+[0-9A-Za-z.-]+)?$`)

type Release struct {
	Version      string    `json:"version"`
	Tag          string    `json:"tag"`
	Name         string    `json:"name"`
	URL          string    `json:"url"`
	Notes        string    `json:"notes"`
	Prerelease   bool      `json:"prerelease"`
	PublishedAt  time.Time `json:"published_at"`
	ArchiveURL   string    `json:"archive_url"`
	ChecksumsURL string    `json:"checksums_url"`
}

type Check struct {
	Repository       string    `json:"repository"`
	CheckedAt        time.Time `json:"checked_at"`
	CurrentVersion   string    `json:"current_version"`
	IncludePreviews  bool      `json:"include_prereleases"`
	UpdateAvailable  bool      `json:"update_available"`
	UpdatesSupported bool      `json:"updates_supported"`
	Reason           string    `json:"reason,omitempty"`
	Latest           *Release  `json:"latest,omitempty"`
}

type githubRelease struct {
	TagName     string        `json:"tag_name"`
	Name        string        `json:"name"`
	HTMLURL     string        `json:"html_url"`
	Body        string        `json:"body"`
	Draft       bool          `json:"draft"`
	Prerelease  bool          `json:"prerelease"`
	PublishedAt time.Time     `json:"published_at"`
	Assets      []githubAsset `json:"assets"`
}

type githubAsset struct {
	Name               string `json:"name"`
	BrowserDownloadURL string `json:"browser_download_url"`
}

type cachedReleases struct {
	releases  []Release
	checkedAt time.Time
	expiresAt time.Time
	etag      string
}

type Checker struct {
	httpClient *http.Client
	url        string
	now        func() time.Time
	mu         sync.Mutex
	cache      cachedReleases
}

func NewChecker() *Checker {
	return &Checker{
		httpClient: &http.Client{Timeout: 15 * time.Second},
		url:        defaultReleasesURL,
		now:        func() time.Time { return time.Now().UTC() },
	}
}

func NewCheckerForTest(url string, client *http.Client) *Checker {
	return &Checker{httpClient: client, url: url, now: func() time.Time { return time.Now().UTC() }}
}

func (c *Checker) Current(current string, includePrereleases bool) Check {
	result := Check{
		Repository:       repositoryURL,
		CurrentVersion:   strings.TrimSpace(current),
		IncludePreviews:  includePrereleases,
		UpdatesSupported: true,
	}
	if _, ok := parseVersion(result.CurrentVersion); !ok {
		result.UpdatesSupported = false
		result.Reason = "In-panel updates are available only for versioned release builds. Development builds must be updated from the source checkout."
	}
	return result
}

func (c *Checker) Check(ctx context.Context, current string, includePrereleases, force bool) (Check, error) {
	result := c.Current(current, includePrereleases)
	if !result.UpdatesSupported {
		return result, nil
	}
	releases, checkedAt, err := c.releases(ctx, force)
	if err != nil {
		return result, err
	}
	result.CheckedAt = checkedAt
	for index := range releases {
		candidate := releases[index]
		if candidate.Prerelease && !includePrereleases {
			continue
		}
		result.Latest = &candidate
		break
	}
	if result.Latest != nil && result.UpdatesSupported {
		result.UpdateAvailable = compareVersions(result.Latest.Version, result.CurrentVersion) > 0
	}
	return result, nil
}

func (c *Checker) Release(ctx context.Context, version string, includePrereleases bool) (Release, error) {
	releases, _, err := c.releases(ctx, false)
	if err != nil {
		return Release{}, err
	}
	version = strings.TrimPrefix(strings.TrimSpace(version), "v")
	for _, release := range releases {
		if release.Version == version && (!release.Prerelease || includePrereleases) {
			return release, nil
		}
	}
	return Release{}, errors.New("the requested Dockside release is not published for the selected update channel")
}

func (c *Checker) releases(ctx context.Context, force bool) ([]Release, time.Time, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	now := c.now()
	if !force && len(c.cache.releases) > 0 && now.Before(c.cache.expiresAt) {
		return append([]Release(nil), c.cache.releases...), c.cache.checkedAt, nil
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.url, nil)
	if err != nil {
		return nil, time.Time{}, err
	}
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("X-GitHub-Api-Version", "2026-03-10")
	req.Header.Set("User-Agent", "Dockside.GG-Game-Panel")
	if c.cache.etag != "" {
		req.Header.Set("If-None-Match", c.cache.etag)
	}
	response, err := c.httpClient.Do(req)
	if err != nil {
		return nil, time.Time{}, fmt.Errorf("check Dockside releases: %w", err)
	}
	defer response.Body.Close()
	if response.StatusCode == http.StatusNotModified && len(c.cache.releases) > 0 {
		c.cache.checkedAt = now
		c.cache.expiresAt = now.Add(5 * time.Minute)
		return append([]Release(nil), c.cache.releases...), now, nil
	}
	if response.StatusCode != http.StatusOK {
		return nil, time.Time{}, fmt.Errorf("check Dockside releases: GitHub returned HTTP %d", response.StatusCode)
	}
	var remote []githubRelease
	decoder := json.NewDecoder(io.LimitReader(response.Body, 4<<20))
	if err := decoder.Decode(&remote); err != nil {
		return nil, time.Time{}, fmt.Errorf("decode Dockside releases: %w", err)
	}
	releases := make([]Release, 0, len(remote))
	for _, item := range remote {
		if item.Draft {
			continue
		}
		version := strings.TrimPrefix(strings.TrimSpace(item.TagName), "v")
		if _, ok := parseVersion(version); !ok {
			continue
		}
		archiveName := "dockside-game-panel-" + version + ".zip"
		var archiveURL, checksumsURL string
		for _, asset := range item.Assets {
			switch asset.Name {
			case archiveName:
				archiveURL = asset.BrowserDownloadURL
			case "SHA256SUMS":
				checksumsURL = asset.BrowserDownloadURL
			}
		}
		if archiveURL == "" || checksumsURL == "" {
			continue
		}
		name := strings.TrimSpace(item.Name)
		if name == "" {
			name = item.TagName
		}
		releases = append(releases, Release{
			Version: version, Tag: item.TagName, Name: name, URL: item.HTMLURL,
			Notes: item.Body, Prerelease: item.Prerelease, PublishedAt: item.PublishedAt,
			ArchiveURL: archiveURL, ChecksumsURL: checksumsURL,
		})
	}
	sort.SliceStable(releases, func(i, j int) bool {
		return compareVersions(releases[i].Version, releases[j].Version) > 0
	})
	c.cache = cachedReleases{
		releases: append([]Release(nil), releases...), checkedAt: now,
		expiresAt: now.Add(5 * time.Minute), etag: response.Header.Get("ETag"),
	}
	return releases, now, nil
}

type version struct {
	major, minor, patch int
	prerelease          []string
}

func parseVersion(input string) (version, bool) {
	matches := semverPattern.FindStringSubmatch(strings.TrimPrefix(strings.TrimSpace(input), "v"))
	if matches == nil {
		return version{}, false
	}
	var result version
	if _, err := fmt.Sscanf(matches[1]+"."+matches[2]+"."+matches[3], "%d.%d.%d", &result.major, &result.minor, &result.patch); err != nil {
		return version{}, false
	}
	if matches[4] != "" {
		result.prerelease = strings.Split(matches[4], ".")
	}
	return result, true
}

func compareVersions(left, right string) int {
	a, okA := parseVersion(left)
	b, okB := parseVersion(right)
	if !okA || !okB {
		return strings.Compare(left, right)
	}
	for _, pair := range [][2]int{{a.major, b.major}, {a.minor, b.minor}, {a.patch, b.patch}} {
		if pair[0] < pair[1] {
			return -1
		}
		if pair[0] > pair[1] {
			return 1
		}
	}
	if len(a.prerelease) == 0 && len(b.prerelease) == 0 {
		return 0
	}
	if len(a.prerelease) == 0 {
		return 1
	}
	if len(b.prerelease) == 0 {
		return -1
	}
	limit := len(a.prerelease)
	if len(b.prerelease) < limit {
		limit = len(b.prerelease)
	}
	for i := 0; i < limit; i++ {
		leftNumber, leftErr := strconv.Atoi(a.prerelease[i])
		rightNumber, rightErr := strconv.Atoi(b.prerelease[i])
		leftNumeric, rightNumeric := leftErr == nil, rightErr == nil
		switch {
		case leftNumeric && rightNumeric && leftNumber != rightNumber:
			if leftNumber < rightNumber {
				return -1
			}
			return 1
		case leftNumeric != rightNumeric:
			if leftNumeric {
				return -1
			}
			return 1
		case a.prerelease[i] != b.prerelease[i]:
			return strings.Compare(a.prerelease[i], b.prerelease[i])
		}
	}
	if len(a.prerelease) < len(b.prerelease) {
		return -1
	}
	if len(a.prerelease) > len(b.prerelease) {
		return 1
	}
	return 0
}
