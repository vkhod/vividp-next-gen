package conversion

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
)

// PageResult describes one output JPEG page.
type PageResult struct {
	Path    string
	PageNum int
}

// ConvertToJPEGPages converts any supported input file to per-page JPEGs.
// Returns sorted page results. PDFs use Ghostscript; images use ImageMagick.
func ConvertToJPEGPages(ctx context.Context, srcPath, outputDir string, dpi, quality int, log *slog.Logger) ([]PageResult, error) {
	if dpi <= 0 {
		dpi = 150
	}
	if quality <= 0 {
		quality = 85
	}

	ext := strings.ToLower(filepath.Ext(srcPath))

	switch ext {
	case ".jpg", ".jpeg":
		return handleJPEGPassthrough(srcPath, outputDir)
	case ".pdf":
		return handlePDF(ctx, srcPath, outputDir, dpi, quality, log)
	case ".tif", ".tiff", ".png", ".bmp", ".gif", ".webp", ".heic":
		return handleImage(ctx, srcPath, outputDir, dpi, quality, log)
	default:
		return handleImage(ctx, srcPath, outputDir, dpi, quality, log)
	}
}

// handleJPEGPassthrough copies the JPEG as page 1 — no conversion needed.
func handleJPEGPassthrough(srcPath, outputDir string) ([]PageResult, error) {
	dst := filepath.Join(outputDir, "page_001.jpg")
	if err := copyFile(srcPath, dst); err != nil {
		return nil, fmt.Errorf("copy JPEG: %w", err)
	}
	return []PageResult{{Path: dst, PageNum: 1}}, nil
}

// handlePDF renders each PDF page to a JPEG using Ghostscript (preferred) or ImageMagick.
func handlePDF(ctx context.Context, srcPath, outputDir string, dpi, quality int, log *slog.Logger) ([]PageResult, error) {
	if _, err := exec.LookPath("gs"); err == nil {
		outPattern := filepath.Join(outputDir, "page_%03d.jpg")
		cmd := exec.CommandContext(ctx,
			"gs",
			"-dNOPAUSE", "-dBATCH",
			"-sDEVICE=jpeg",
			fmt.Sprintf("-r%d", dpi),
			fmt.Sprintf("-dJPEGQ=%d", quality),
			fmt.Sprintf("-sOutputFile=%s", outPattern),
			srcPath,
		)
		cmd.Stdout, cmd.Stderr = os.Stdout, os.Stderr
		if err := cmd.Run(); err == nil {
			return collectJPEGs(outputDir)
		}
		log.Warn("gs failed, falling back to ImageMagick", "file", filepath.Base(srcPath))
	}
	return handleImage(ctx, srcPath, outputDir, dpi, quality, log)
}

// handleImage converts any image format to JPEG pages using ImageMagick.
func handleImage(ctx context.Context, srcPath, outputDir string, dpi, quality int, log *slog.Logger) ([]PageResult, error) {
	if _, err := exec.LookPath("convert"); err != nil {
		return nil, fmt.Errorf("no conversion tool available — install Ghostscript (gs) or ImageMagick (convert)")
	}

	outPattern := filepath.Join(outputDir, "page_%03d.jpg")
	cmd := exec.CommandContext(ctx,
		"convert",
		"-density", fmt.Sprintf("%d", dpi),
		"-quality", fmt.Sprintf("%d", quality),
		srcPath,
		outPattern,
	)
	cmd.Stdout, cmd.Stderr = os.Stdout, os.Stderr
	if err := cmd.Run(); err != nil {
		return nil, fmt.Errorf("ImageMagick convert: %w", err)
	}

	pages, err := collectJPEGs(outputDir)
	if err != nil {
		return nil, err
	}
	// ImageMagick single-image output uses page_000.jpg
	if len(pages) == 1 && pages[0].PageNum == 0 {
		renamed := filepath.Join(outputDir, "page_001.jpg")
		os.Rename(pages[0].Path, renamed)
		pages[0].Path = renamed
		pages[0].PageNum = 1
	}
	return pages, nil
}

// collectJPEGs scans outputDir for page_*.jpg files and returns them sorted.
func collectJPEGs(outputDir string) ([]PageResult, error) {
	matches, err := filepath.Glob(filepath.Join(outputDir, "page_*.jpg"))
	if err != nil {
		return nil, fmt.Errorf("glob JPEGs: %w", err)
	}
	if len(matches) == 0 {
		return nil, fmt.Errorf("no JPEG pages generated in %s", outputDir)
	}
	sort.Strings(matches)

	pages := make([]PageResult, len(matches))
	for i, p := range matches {
		// Extract page number from filename: page_001.jpg → 1
		base := filepath.Base(p)
		var n int
		fmt.Sscanf(base, "page_%d.jpg", &n)
		pages[i] = PageResult{Path: p, PageNum: n}
	}
	return pages, nil
}

func copyFile(src, dst string) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()

	out, err := os.Create(dst)
	if err != nil {
		return err
	}
	defer out.Close()

	_, err = out.ReadFrom(in)
	return err
}
