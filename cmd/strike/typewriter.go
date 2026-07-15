package main

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"github.com/lajosnagyuk/ayfor/internal/export"
	"github.com/lajosnagyuk/ayfor/internal/typewriter"
)

func cmdTypewriter(args []string) error {
	if len(args) == 0 {
		usage()
	}
	switch args[0] {
	case "list":
		if len(args) != 1 {
			usage()
		}
		r, err := typewriter.DefaultRegistry()
		if err != nil {
			return err
		}
		items, err := r.List()
		if err != nil {
			return err
		}
		for _, item := range items {
			kind := "installed"
			if item.Builtin {
				kind = "built-in"
			}
			fmt.Printf("%-12s %-32s %-10s %s\n", kind, item.Ref.ID, item.Ref.Version, item.Ref.Digest)
		}
		return nil
	case "inspect":
		if len(args) != 2 {
			usage()
		}
		p, err := typewriter.LoadArchive(args[1])
		if err != nil {
			return err
		}
		return printPackage(p)
	case "pack":
		if len(args) != 3 {
			usage()
		}
		return packSource(args[1], args[2])
	case "install":
		if len(args) != 2 {
			usage()
		}
		r, err := typewriter.DefaultRegistry()
		if err != nil {
			return err
		}
		item, err := r.Install(args[1])
		if err != nil {
			return err
		}
		fmt.Printf("installed %s %s (%s)\n", item.Name, item.Ref.Version, item.Ref.Digest)
		return nil
	case "remove":
		if len(args) != 4 {
			usage()
		}
		r, err := typewriter.DefaultRegistry()
		if err != nil {
			return err
		}
		ref := typewriter.Ref{ID: args[1], Version: args[2], Digest: args[3]}
		if err := r.Remove(ref); err != nil {
			return err
		}
		fmt.Printf("removed %s\n", ref)
		return nil
	case "export-builtin":
		if len(args) != 3 {
			usage()
		}
		p, err := typewriter.Builtin(args[1])
		if err != nil {
			return err
		}
		data, err := p.Archive()
		if err != nil {
			return err
		}
		return export.AtomicCreateFile(args[2], data)
	default:
		return fmt.Errorf("unknown typewriter command %q", args[0])
	}
}

func packSource(source, target string) error {
	abs, err := filepath.Abs(source)
	if err != nil {
		return err
	}
	info, err := os.Lstat(abs)
	if err != nil {
		return err
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.IsDir() {
		return errors.New("typewriter source must be a real directory, not a symlink")
	}
	root := filepath.Base(filepath.Clean(abs))
	if strings.ContainsRune(root, '\\') {
		return errors.New("invalid source directory name")
	}
	rootFS, err := os.OpenRoot(abs)
	if err != nil {
		return err
	}
	defer rootFS.Close()
	fsys := rootFS.FS()
	openedInfo, err := fs.Stat(fsys, ".")
	if err != nil {
		return err
	}
	if !os.SameFile(info, openedInfo) {
		return errors.New("typewriter source directory changed while it was opened")
	}
	p, err := typewriter.LoadSourceRootFS(fsys, root)
	if err != nil {
		return err
	}
	// Packing is a publication boundary, not merely ZIP creation. Reject a
	// source whose font, calibration, WAVs, licences, or provenance cannot be
	// fully materialized before writing a distributable artifact.
	if _, err := p.Profile(); err != nil {
		return err
	}
	data, err := p.Archive()
	if err != nil {
		return err
	}
	if err := export.AtomicCreateFile(target, data); err != nil {
		return err
	}
	fmt.Printf("packed %s %s (%s)\n", p.Manifest.Name, p.Manifest.Version, p.Digest)
	return nil
}

func printPackage(p *typewriter.Package) error {
	if _, err := p.Profile(); err != nil {
		return err
	}
	m := p.Manifest
	fmt.Printf("name:       %s\n", m.Name)
	fmt.Printf("id:         %s\n", m.ID)
	fmt.Printf("version:    %s\n", m.Version)
	fmt.Printf("digest:     %s\n", p.Digest)
	fmt.Printf("publisher:  %s\n", m.Publisher)
	fmt.Printf("fidelity:   %s\n", m.Fidelity)
	fmt.Printf("engine:     %s/%d\n", m.Engine.ID, m.Engine.Version)
	fmt.Printf("pitch:      %d cpi\n", m.Geometry.PitchCPI)
	fmt.Printf("font:       %q\n", m.Typeface.Path)
	fmt.Printf("sounds:     %d hammer samples\n", len(m.Sound.Hammer))
	return nil
}
