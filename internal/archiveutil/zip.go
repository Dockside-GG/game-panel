package archiveutil

import (
	"archive/tar"
	"archive/zip"
	"compress/gzip"
	"errors"
	"io"
	"regexp"
	"strings"
)

// ZipTarGzip converts a Dockside tar-gzip backup to ZIP without buffering the
// complete archive in memory.
func ZipTarGzip(source io.Reader) io.ReadCloser {
	reader, writer := io.Pipe()
	go func() {
		var resultErr error
		compressed, err := gzip.NewReader(source)
		if err != nil {
			_ = writer.CloseWithError(err)
			return
		}
		archive := tar.NewReader(compressed)
		zipped := zip.NewWriter(writer)
		for {
			header, nextErr := archive.Next()
			if errors.Is(nextErr, io.EOF) {
				break
			}
			if nextErr != nil {
				resultErr = nextErr
				break
			}
			if header.Typeflag != tar.TypeReg && header.Typeflag != tar.TypeRegA {
				continue
			}
			target, createErr := zipped.CreateHeader(&zip.FileHeader{
				Name: header.Name, Method: zip.Deflate, Modified: header.ModTime,
			})
			if createErr != nil {
				resultErr = createErr
				break
			}
			if _, copyErr := io.CopyN(target, archive, header.Size); copyErr != nil {
				resultErr = copyErr
				break
			}
		}
		if closeErr := compressed.Close(); resultErr == nil {
			resultErr = closeErr
		}
		if closeErr := zipped.Close(); resultErr == nil {
			resultErr = closeErr
		}
		_ = writer.CloseWithError(resultErr)
	}()
	return reader
}

func SafeFilename(value string) string {
	value = strings.TrimSpace(value)
	value = regexp.MustCompile(`[^A-Za-z0-9._-]+`).ReplaceAllString(value, "-")
	value = strings.Trim(value, ".-_")
	if value == "" {
		return "dockside-backup"
	}
	if len(value) > 100 {
		value = value[:100]
	}
	return value
}
