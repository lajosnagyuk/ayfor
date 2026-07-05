package main

import (
	"runtime"
	"strconv"
	"strings"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/theme"

	"github.com/lajosnagyuk/ayfor/internal/units"
)

const defaultWindowHeight = 1000

func a4ContentSizeForHeight(height float32) fyne.Size {
	if height < 1 {
		height = defaultWindowHeight
	}
	aspect := float32(units.PaperWidthMM / units.PaperHeightMM)
	return fyne.NewSize(height*aspect, height)
}

func a4WindowSizeForHeight(height float32) fyne.Size {
	return a4ContentSizeForHeight(height).Add(fyne.NewSize(0, menuBarHeight()))
}

func (u *ui) resizeA4Window(height float32) {
	u.win.Resize(a4WindowSizeForHeight(height))
}

func (u *ui) toggleFullscreen() {
	u.win.SetFullScreen(!u.win.FullScreen())
	u.applyDankChrome()
}

// configureDesktopScale runs before Fyne creates the first window. Linux
// desktops sometimes report fractional/scaled KDE sessions as scale 1, leaving
// menus tiny on high-DPI monitors. Prefer an explicit app override, then the
// KDE scale environment on Linux, then Fyne's DPI auto-detection.
func configureDesktopScale(env []string, setenv func(string, string) error) {
	configureDesktopScaleForGOOS(runtime.GOOS, env, setenv)
}

func configureDesktopScaleForGOOS(goos string, env []string, setenv func(string, string) error) {
	if envLookup(env, "FYNE_SCALE") != "" {
		return
	}
	if scale := scaleFromEnv(envLookup(env, "AYFOR_UI_SCALE")); scale != "" {
		_ = setenv("FYNE_SCALE", scale)
		return
	}
	if goos != "linux" {
		return
	}
	kde := strings.Contains(strings.ToLower(envLookup(env, "XDG_CURRENT_DESKTOP")), "kde")
	if kde {
		if scale := scaleFromEnv(envLookup(env, "QT_SCALE_FACTOR")); scale != "" {
			_ = setenv("FYNE_SCALE", scale)
			return
		}
		if scale := scaleFromScreenFactors(envLookup(env, "QT_SCREEN_SCALE_FACTORS")); scale != "" {
			_ = setenv("FYNE_SCALE", scale)
			return
		}
	}
	_ = setenv("FYNE_SCALE", "auto")
}

func menuBarHeight() float32 {
	if runtime.GOOS == "darwin" {
		return 0
	}
	return fyne.MeasureText("M", theme.TextSize(), fyne.TextStyle{}).Height + theme.InnerPadding()
}

func envLookup(env []string, key string) string {
	prefix := key + "="
	for _, kv := range env {
		if strings.HasPrefix(kv, prefix) {
			return strings.TrimPrefix(kv, prefix)
		}
	}
	return ""
}

func scaleFromEnv(v string) string {
	v = strings.TrimSpace(v)
	if v == "" {
		return ""
	}
	f, err := strconv.ParseFloat(v, 32)
	if err != nil || f <= 0 {
		return ""
	}
	return strconv.FormatFloat(f, 'f', -1, 32)
}

func scaleFromScreenFactors(v string) string {
	var best float64
	for _, part := range strings.Split(v, ";") {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		if i := strings.LastIndex(part, "="); i >= 0 {
			part = part[i+1:]
		}
		scale := scaleFromEnv(part)
		if scale == "" {
			continue
		}
		f, _ := strconv.ParseFloat(scale, 64)
		if f > best {
			best = f
		}
	}
	if best == 0 {
		return ""
	}
	return strconv.FormatFloat(best, 'f', -1, 64)
}
