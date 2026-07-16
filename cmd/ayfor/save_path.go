package main

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/dialog"
	"fyne.io/fyne/v2/widget"
)

type saveTargetFunc func(folder, name string) (string, error)

func targetWithExtension(extension string) saveTargetFunc {
	return func(folder, name string) (string, error) {
		target, err := resolveSaveTarget(folder, name)
		if err != nil {
			return "", err
		}
		if strings.ToLower(filepath.Ext(target)) != extension {
			target += extension
		}
		return target, nil
	}
}

func exportTarget(folder, name string) (string, error) {
	target, err := resolveSaveTarget(folder, name)
	if err != nil {
		return "", err
	}
	switch strings.ToLower(filepath.Ext(target)) {
	case ".txt", ".md", ".docx", ".pdf":
		return target, nil
	default:
		return "", errors.New("choose a .txt, .md, .docx or .pdf name")
	}
}

func resolveSaveTarget(folder, name string) (string, error) {
	if strings.TrimSpace(folder) == "" {
		return "", errors.New("choose a folder")
	}
	cleanName := strings.TrimSpace(name)
	if cleanName == "" || cleanName == "." || cleanName == ".." || filepath.Base(cleanName) != cleanName || strings.ContainsAny(cleanName, `/\\`) {
		return "", errors.New("enter a file name, not a path")
	}
	info, err := os.Lstat(folder)
	if err != nil {
		return "", fmt.Errorf("folder: %w", err)
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.IsDir() {
		return "", errors.New("folder must be a real directory, not a symlink")
	}
	return filepath.Join(folder, cleanName), nil
}

// showPathSaveDialog deliberately returns a path without opening it. Fyne's
// FileSave API opens (and truncates) the destination before invoking its
// callback, which makes it impossible to preserve an existing file if the
// subsequent render or rename fails.
func (u *ui) showPathSaveDialog(title, confirmText, initialDir, initialName string, targetFn saveTargetFunc, onChosen func(string)) {
	if err := os.MkdirAll(initialDir, 0o755); err != nil {
		u.showError(err)
		return
	}
	folder := widget.NewEntry()
	folder.SetText(initialDir)
	name := widget.NewEntry()
	name.SetText(initialName)

	browse := widget.NewButton("Choose...", func() {
		fd := dialog.NewFolderOpen(func(uri fyne.ListableURI, err error) {
			if err != nil {
				u.showError(err)
				return
			}
			if uri != nil {
				folder.SetText(uri.Path())
			}
		}, u.win)
		u.locateDialog(fd, folder.Text)
		fd.Show()
	})
	folderRow := container.NewBorder(nil, nil, nil, browse, folder)
	items := []*widget.FormItem{
		{Text: "Folder", Widget: folderRow},
		{Text: "Name", Widget: name},
	}

	u.pushModal()
	fd := dialog.NewForm(title, confirmText, "Cancel", items, func(ok bool) {
		u.popModal()
		if !ok {
			return
		}
		target, err := targetFn(folder.Text, name.Text)
		if err != nil {
			u.showError(err)
			return
		}
		info, err := os.Lstat(target)
		switch {
		case err == nil && (info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular()):
			u.showError(errors.New("destination must be a regular file"))
		case err == nil:
			u.showError(fmt.Errorf("%s already exists; choose a new name", target))
		case os.IsNotExist(err):
			onChosen(target)
		default:
			u.showError(err)
		}
	}, u.win)
	fd.Resize(fyne.NewSize(680, 220))
	fd.Show()
}
