package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/dockside-gg/game-panel/internal/templates"
	"gopkg.in/yaml.v3"
)

type workItem struct {
	sourceKind string
	category   string
	item       templates.CatalogItem
}

type result struct {
	entry templates.TemplateEntry
	err   error
	label string
}

func main() {
	sourceDir := flag.String("source-dir", "templates/sources", "directory containing local catalog index snapshots")
	output := flag.String("output", "templates/library/generated/catalog.json", "generated offline bundle path")
	concurrency := flag.Int("concurrency", 12, "maximum simultaneous definition downloads")
	flag.Parse()
	if *concurrency < 1 || *concurrency > 32 {
		fatal(errors.New("concurrency must be between 1 and 32"))
	}
	if err := run(*sourceDir, *output, *concurrency); err != nil {
		fatal(err)
	}
}

func run(sourceDir, output string, concurrency int) error {
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Minute)
	defer cancel()
	client := &http.Client{
		Timeout: 45 * time.Second,
		CheckRedirect: func(request *http.Request, via []*http.Request) error {
			if len(via) >= 3 {
				return errors.New("too many redirects")
			}
			return validateURL(request.URL.String())
		},
	}

	bundle := templates.Bundle{
		FormatVersion: templates.BundleFormatVersion,
		GeneratedAt:   time.Now().UTC().Format(time.RFC3339),
	}
	work := make([]workItem, 0, 700)
	for _, sourceKind := range []string{"pelican", "pterodactyl"} {
		path := filepath.Join(sourceDir, sourceKind+".json")
		document, err := os.ReadFile(path)
		if err != nil {
			return fmt.Errorf("read %s index: %w", sourceKind, err)
		}
		digest := sha256.Sum256(document)
		var index templates.CatalogIndex
		if err := json.Unmarshal(document, &index); err != nil {
			return fmt.Errorf("decode %s index: %w", sourceKind, err)
		}
		count := 0
		for _, nest := range index.Nests {
			for _, item := range nest.Eggs {
				work = append(work, workItem{sourceKind: sourceKind, category: nest.Type, item: item})
				count++
			}
		}
		bundle.Sources = append(bundle.Sources, templates.BundleSource{
			Kind: sourceKind, Digest: hex.EncodeToString(digest[:]), Count: count,
		})
	}

	jobs := make(chan workItem)
	results := make(chan result)
	var workers sync.WaitGroup
	for range concurrency {
		workers.Add(1)
		go func() {
			defer workers.Done()
			for item := range jobs {
				entry, err := resolve(ctx, client, item)
				results <- result{entry: entry, err: err, label: item.sourceKind + "/" + item.category + "/" + item.item.Egg.Name}
			}
		}()
	}
	go func() {
		defer close(jobs)
		for _, item := range work {
			jobs <- item
		}
	}()
	go func() {
		workers.Wait()
		close(results)
	}()

	failures := make([]string, 0)
	seenSlugs := make(map[string]struct{}, len(work))
	for resolved := range results {
		if resolved.err != nil {
			failures = append(failures, resolved.label+": "+resolved.err.Error())
			continue
		}
		if _, exists := seenSlugs[resolved.entry.Slug]; exists {
			resolved.entry.Slug += "-" + resolved.entry.SourceDigest[:10]
		}
		seenSlugs[resolved.entry.Slug] = struct{}{}
		bundle.Templates = append(bundle.Templates, resolved.entry)
		if len(bundle.Templates)%50 == 0 {
			fmt.Fprintf(os.Stderr, "resolved %d/%d templates\n", len(bundle.Templates), len(work))
		}
	}
	if len(failures) > 0 {
		sort.Strings(failures)
		return fmt.Errorf("%d template definitions failed validation:\n%s", len(failures), strings.Join(failures, "\n"))
	}
	if len(bundle.Templates) != len(work) {
		return fmt.Errorf("resolved %d templates, expected %d", len(bundle.Templates), len(work))
	}
	sort.Slice(bundle.Templates, func(i, j int) bool {
		return bundle.Templates[i].Slug < bundle.Templates[j].Slug
	})
	encoded, err := json.Marshal(bundle)
	if err != nil {
		return fmt.Errorf("encode bundle: %w", err)
	}
	if err := os.MkdirAll(filepath.Dir(output), 0o755); err != nil {
		return fmt.Errorf("create output directory: %w", err)
	}
	temp := output + ".tmp"
	if err := os.WriteFile(temp, encoded, 0o644); err != nil {
		return fmt.Errorf("write temporary bundle: %w", err)
	}
	if err := os.Rename(temp, output); err != nil {
		return fmt.Errorf("replace bundle: %w", err)
	}
	fmt.Fprintf(os.Stderr, "wrote %d templates to %s\n", len(bundle.Templates), output)
	return nil
}

func resolve(ctx context.Context, client *http.Client, item workItem) (templates.TemplateEntry, error) {
	downloadURL := strings.ReplaceAll(item.item.DownloadURL, "#/", "%23/")
	if err := validateURL(downloadURL); err != nil {
		return templates.TemplateEntry{}, err
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, downloadURL, nil)
	if err != nil {
		return templates.TemplateEntry{}, err
	}
	request.Header.Set("Accept", "application/json")
	request.Header.Set("User-Agent", "Dockside-Template-Bundler/1")
	response, err := client.Do(request)
	if err != nil {
		return templates.TemplateEntry{}, fmt.Errorf("download: %w", err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return templates.TemplateEntry{}, fmt.Errorf("download returned HTTP %d", response.StatusCode)
	}
	document, err := io.ReadAll(io.LimitReader(response.Body, 2<<20))
	if err != nil {
		return templates.TemplateEntry{}, fmt.Errorf("read definition: %w", err)
	}
	if len(document) == 0 || len(document) >= 2<<20 {
		return templates.TemplateEntry{}, errors.New("definition is empty or exceeds 2 MiB")
	}
	sourceDocument := document
	if !json.Valid(document) {
		var yamlDocument any
		if err := yaml.Unmarshal(document, &yamlDocument); err != nil {
			return templates.TemplateEntry{}, fmt.Errorf("definition is neither valid JSON nor YAML: %w", err)
		}
		document, err = json.Marshal(yamlDocument)
		if err != nil {
			return templates.TemplateEntry{}, fmt.Errorf("convert YAML definition to JSON: %w", err)
		}
	}
	entry, err := templates.Normalize(item.sourceKind, item.category, item.item.DownloadURL, document)
	if err != nil {
		return templates.TemplateEntry{}, err
	}
	if entry.Description == "" {
		entry.Description = strings.TrimSpace(item.item.Egg.Description)
		entry.CanonicalDocument.Description = entry.Description
	}
	digest := sha256.Sum256(sourceDocument)
	entry.SourceDigest = hex.EncodeToString(digest[:])
	return entry, nil
}

func validateURL(raw string) error {
	parsed, err := url.Parse(raw)
	if err != nil {
		return fmt.Errorf("invalid URL: %w", err)
	}
	if parsed.Scheme != "https" || !strings.EqualFold(parsed.Hostname(), "raw.githubusercontent.com") || parsed.User != nil {
		return errors.New("definition URL must be HTTPS on raw.githubusercontent.com")
	}
	return nil
}

func fatal(err error) {
	fmt.Fprintln(os.Stderr, "template-bundler:", err)
	os.Exit(1)
}
