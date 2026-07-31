package worker

import (
	"archive/tar"
	"archive/zip"
	"bytes"
	"compress/gzip"
	"io"
	"testing"

	"github.com/dockside-gg/game-panel/internal/archiveutil"
)

func TestZipArchiveStreamsRestorableFiles(t *testing.T) {
	t.Parallel()
	var source bytes.Buffer
	compressed := gzip.NewWriter(&source)
	archive := tar.NewWriter(compressed)
	content := []byte("dockside backup")
	if err := archive.WriteHeader(&tar.Header{
		Name: "Saved/world.dat", Mode: 0o640, Size: int64(len(content)),
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := archive.Write(content); err != nil {
		t.Fatal(err)
	}
	if err := archive.Close(); err != nil {
		t.Fatal(err)
	}
	if err := compressed.Close(); err != nil {
		t.Fatal(err)
	}

	stream := archiveutil.ZipTarGzip(bytes.NewReader(source.Bytes()))
	defer stream.Close()
	zipped, err := io.ReadAll(stream)
	if err != nil {
		t.Fatal(err)
	}
	reader, err := zip.NewReader(bytes.NewReader(zipped), int64(len(zipped)))
	if err != nil {
		t.Fatal(err)
	}
	if len(reader.File) != 1 || reader.File[0].Name != "Saved/world.dat" {
		t.Fatalf("ZIP entries = %#v", reader.File)
	}
	file, err := reader.File[0].Open()
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	got, err := io.ReadAll(file)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, content) {
		t.Fatalf("ZIP content = %q", got)
	}
}
