package safefile

import (
	"os"
	"path/filepath"
	"testing"
)

func TestReadRegularIsBoundedAndRejectsSymlink(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "input")
	if err := os.WriteFile(path, []byte("hello"), 0o600); err != nil {
		t.Fatal(err)
	}
	got, err := ReadRegular(path, 5)
	if err != nil || string(got) != "hello" {
		t.Fatalf("read = %q, %v", got, err)
	}
	if _, err := ReadRegular(path, 4); err == nil {
		t.Fatal("accepted oversized input")
	}
	link := filepath.Join(dir, "link")
	if err := os.Symlink(path, link); err == nil {
		if _, err := ReadRegular(link, 5); err == nil {
			t.Fatal("accepted symlink input")
		}
	}
}
