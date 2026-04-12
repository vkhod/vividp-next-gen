// ═══════════════════════════════════════════════════════════════════════════════
// ingestion/converter.go
// PDF / image → per-page TIF conversion.
// Tries ImageMagick (convert) → Ghostscript (gs) → copy-as-is fallback.
// ═══════════════════════════════════════════════════════════════════════════════
package ingestion

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

// ConversionResult describes the output of converting one source file.
type ConversionResult struct {
	PagePaths []string // absolute paths to per-page TIF files, sorted
	PageCount int
	WidthPx   int // first page width (used for all pages if consistent)
	HeightPx  int // first page height
}

// ConvertToTIF converts any supported input format to per-page 300dpi greyscale TIFs.
// Output files are written to outputDir as page_001.tif, page_002.tif, ...
// Returns paths to all generated TIF files.
func ConvertToTIF(ctx context.Context, srcPath, outputDir string, log *slog.Logger) (*ConversionResult, error) {
	ext := strings.ToLower(filepath.Ext(srcPath))

	switch ext {
	case ".tif", ".tiff":
		return handleTIF(ctx, srcPath, outputDir)
	case ".pdf":
		return handlePDF(ctx, srcPath, outputDir, log)
	case ".jpg", ".jpeg", ".png", ".bmp":
		return handleImage(ctx, srcPath, outputDir, log)
	default:
		// Unknown format — attempt ImageMagick, fallback to copy
		result, err := tryImageMagick(ctx, srcPath, outputDir)
		if err != nil {
			return copyAsOnePage(srcPath, outputDir, log)
		}
		return result, nil
	}
}

// handleTIF — input is already TIF. Copy to output dir as page_001.tif.
func handleTIF(_ context.Context, srcPath, outputDir string) (*ConversionResult, error) {
	dst := filepath.Join(outputDir, "page_001.tif")
	if err := copyFile(srcPath, dst); err != nil {
		return nil, fmt.Errorf("copy TIF: %w", err)
	}
	info, _ := os.Stat(dst)
	_ = info
	return &ConversionResult{PagePaths: []string{dst}, PageCount: 1}, nil
}

// handlePDF — try Ghostscript first (better PDF fidelity), then ImageMagick.
func handlePDF(ctx context.Context, srcPath, outputDir string, log *slog.Logger) (*ConversionResult, error) {
	// Try Ghostscript — preferred for PDF
	if _, err := exec.LookPath("gs"); err == nil {
		outPattern := filepath.Join(outputDir, "page_%03d.tif")
		cmd := exec.CommandContext(ctx,
			"gs",
			"-dNOPAUSE", "-dBATCH",
			"-sDEVICE=tiffg4",        // CCITT Group 4 compression — compact B&W
			"-r300",                   // 300 dpi
			"-dGRAYSCALE",
			fmt.Sprintf("-sOutputFile=%s", outPattern),
			srcPath,
		)
		cmd.Stdout, cmd.Stderr = os.Stdout, os.Stderr
		if err := cmd.Run(); err == nil {
			return collectTIFs(outputDir)
		}
		log.Warn("gs failed, falling back to ImageMagick", "file", filepath.Base(srcPath))
	}

	// Fallback: ImageMagick
	result, err := tryImageMagick(ctx, srcPath, outputDir)
	if err != nil {
		return copyAsOnePage(srcPath, outputDir, log)
	}
	return result, nil
}

// handleImage — single-page image formats.
func handleImage(ctx context.Context, srcPath, outputDir string, log *slog.Logger) (*ConversionResult, error) {
	result, err := tryImageMagick(ctx, srcPath, outputDir)
	if err != nil {
		return copyAsOnePage(srcPath, outputDir, log)
	}
	return result, nil
}

// tryImageMagick runs ImageMagick's convert command.
func tryImageMagick(ctx context.Context, srcPath, outputDir string) (*ConversionResult, error) {
	if _, err := exec.LookPath("convert"); err != nil {
		return nil, fmt.Errorf("ImageMagick not found")
	}

	outPattern := filepath.Join(outputDir, "page_%03d.tif")
	cmd := exec.CommandContext(ctx,
		"convert",
		"-density", "300",
		"-type", "Grayscale",
		"-compress", "Group4",
		srcPath,
		outPattern,
	)
	cmd.Stdout, cmd.Stderr = os.Stdout, os.Stderr

	if err := cmd.Run(); err != nil {
		return nil, fmt.Errorf("convert: %w", err)
	}
	return collectTIFs(outputDir)
}

// copyAsOnePage is the last-resort fallback — copies the source as page_001.tif.
// The file may not be a real TIF but at least the job is tracked and won't disappear.
func copyAsOnePage(srcPath, outputDir string, log *slog.Logger) (*ConversionResult, error) {
	dst := filepath.Join(outputDir, "page_001.tif")
	if err := copyFile(srcPath, dst); err != nil {
		return nil, fmt.Errorf("fallback copy: %w", err)
	}
	log.Warn("used fallback copy — file may not be valid TIF", "file", filepath.Base(srcPath))
	return &ConversionResult{PagePaths: []string{dst}, PageCount: 1}, nil
}

// collectTIFs scans outputDir and returns all page_*.tif files in sorted order.
func collectTIFs(outputDir string) (*ConversionResult, error) {
	pattern := filepath.Join(outputDir, "page_*.tif")
	matches, err := filepath.Glob(pattern)
	if err != nil {
		return nil, fmt.Errorf("glob TIFs: %w", err)
	}
	if len(matches) == 0 {
		return nil, fmt.Errorf("no TIF pages generated in %s", outputDir)
	}
	sort.Strings(matches)
	return &ConversionResult{
		PagePaths: matches,
		PageCount: len(matches),
	}, nil
}

// copyFile copies src to dst.
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
