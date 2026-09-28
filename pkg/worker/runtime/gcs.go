package runtime

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"time"

	"golang.org/x/sync/singleflight"
)

var gcsDownloadSF singleflight.Group

var gcsDownloadURL = func(bucket, object string) string {
	return fmt.Sprintf("https://storage.googleapis.com/%s/%s", bucket, object)
}

// DownloadFromGCS downloads an object from a public GCS bucket to a local file.
// Concurrent downloads to the same destination are deduplicated and written atomically.
func DownloadFromGCS(ctx context.Context, bucket, object, destPath string, logger *slog.Logger) error {
	_, err, _ := gcsDownloadSF.Do(destPath, func() (any, error) {
		return nil, downloadFromGCSOnce(ctx, bucket, object, destPath, logger)
	})
	return err
}

func downloadFromGCSOnce(ctx context.Context, bucket, object, destPath string, logger *slog.Logger) error {
	if ok, err := artifactFileReady(destPath); err != nil {
		return err
	} else if ok {
		return nil
	}

	gcsURL := gcsDownloadURL(bucket, object)
	if logger != nil {
		logger.Info("downloading artifact from GCS", "url", gcsURL, "dest", destPath)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, gcsURL, nil)
	if err != nil {
		return fmt.Errorf("create download request: %w", err)
	}

	client := &http.Client{Timeout: 60 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("execute GCS HTTP download: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		bodyBytes, _ := io.ReadAll(io.LimitReader(resp.Body, 1024))
		return fmt.Errorf("GCS returned status %d: %s", resp.StatusCode, string(bodyBytes))
	}

	dir := filepath.Dir(destPath)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("create destination directory: %w", err)
	}

	tmpPath := destPath + ".part"
	destFile, err := os.OpenFile(tmpPath, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o644)
	if err != nil {
		return fmt.Errorf("create temporary destination file: %w", err)
	}

	written, copyErr := io.Copy(destFile, resp.Body)
	closeErr := destFile.Close()
	if copyErr != nil {
		_ = os.Remove(tmpPath)
		return fmt.Errorf("write file: %w", copyErr)
	}
	if closeErr != nil {
		_ = os.Remove(tmpPath)
		return fmt.Errorf("close temporary destination file: %w", closeErr)
	}

	if resp.ContentLength > 0 && written != resp.ContentLength {
		_ = os.Remove(tmpPath)
		return fmt.Errorf("incomplete download: wrote %d bytes, expected %d", written, resp.ContentLength)
	}
	if written == 0 {
		_ = os.Remove(tmpPath)
		return fmt.Errorf("downloaded artifact is empty")
	}

	if err := os.Rename(tmpPath, destPath); err != nil {
		_ = os.Remove(tmpPath)
		return fmt.Errorf("publish downloaded artifact: %w", err)
	}

	if logger != nil {
		logger.Info("downloaded artifact successfully", "url", gcsURL, "bytes", written)
	}
	return nil
}

func artifactFileReady(path string) (bool, error) {
	info, err := os.Stat(path)
	if err != nil {
		if os.IsNotExist(err) {
			return false, nil
		}
		return false, err
	}
	return info.Size() > 0, nil
}
