package util

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"math"
	"net/http"
	"os"
	"path/filepath"
	"sync"
	"time"

	"govd/models"
	"govd/util/av"
)

func DefaultConfig() *models.DownloadConfig {
	return &models.DownloadConfig{
		ChunkSize:     10 * 1024 * 1024, // 10MB
		Concurrency:   4,
		Timeout:       30 * time.Second,
		DownloadDir:   "downloads",
		RetryAttempts: 3,
		RetryDelay:    2 * time.Second,
		Remux:         true,
	}
}

func DownloadFile(
	ctx context.Context,
	URLList []string,
	fileName string,
	config *models.DownloadConfig,
) (string, error) {
	if config == nil {
		config = DefaultConfig()
	}

	var errs []error
	for _, fileURL := range URLList {
		select {
		case <-ctx.Done():
			return "", ctx.Err()
		default:
			// create the download directory if it doesn't exist
			if err := ensureDownloadDir(config.DownloadDir); err != nil {
				return "", err
			}

			filePath := filepath.Join(config.DownloadDir, fileName)
			err := runChunkedDownload(ctx, fileURL, filePath, config)
			if err != nil {
				errs = append(errs, err)
				continue
			}

			if config.Remux {
				err := av.RemuxFile(filePath)
				if err != nil {
					return "", fmt.Errorf("remuxing failed: %w", err)
				}
			}
			return filePath, nil
		}
	}

	return "", fmt.Errorf("%w: %v", ErrDownloadFailed, errs)
}

func DownloadFileWithSegments(
	ctx context.Context,
	segmentURLs []string,
	fileName string,
	config *models.DownloadConfig,
) (string, error) {
	if config == nil {
		config = DefaultConfig()
	}
	if err := ensureDownloadDir(config.DownloadDir); err != nil {
		return "", err
	}
	tempDir := filepath.Join(config.DownloadDir, "segments_"+time.Now().Format("20060102_150405"))
	if err := os.MkdirAll(tempDir, 0755); err != nil {
		return "", fmt.Errorf("failed to create temporary directory: %w", err)
	}
	downloadedFiles, err := DownloadSegments(ctx, segmentURLs, config)
	if err != nil {
		os.RemoveAll(tempDir)
		return "", fmt.Errorf("failed to download segments: %w", err)
	}
	mergedFilePath, err := MergeSegmentFiles(ctx, downloadedFiles, fileName, config)
	if err != nil {
		os.RemoveAll(tempDir)
		return "", fmt.Errorf("failed to merge segments: %w", err)
	}
	if err := os.RemoveAll(tempDir); err != nil {
		return "", fmt.Errorf("failed to remove temporary directory: %w", err)
	}
	return mergedFilePath, nil
}

func DownloadFileInMemory(
	ctx context.Context,
	URLList []string,
	config *models.DownloadConfig,
) (*bytes.Reader, error) {
	if config == nil {
		config = DefaultConfig()
	}

	var errs []error
	for _, fileURL := range URLList {
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		default:
			data, err := downloadInMemory(ctx, fileURL, config.Timeout)
			if err != nil {
				errs = append(errs, err)
				continue
			}
			return bytes.NewReader(data), nil
		}
	}

	return nil, fmt.Errorf("%w: %v", ErrDownloadFailed, errs)
}

func downloadInMemory(ctx context.Context, fileURL string, timeout time.Duration) ([]byte, error) {
	reqCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	req, err := http.NewRequestWithContext(reqCtx, http.MethodGet, fileURL, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}

	session := GetHTTPSession()
	resp, err := session.Do(req)
	if err != nil {
		return nil, fmt.Errorf("failed to download file: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("unexpected status code: %d", resp.StatusCode)
	}

	return io.ReadAll(resp.Body)
}

func ensureDownloadDir(dir string) error {
	if _, err := os.Stat(dir); os.IsNotExist(err) {
		if err := os.MkdirAll(dir, 0755); err != nil {
			return fmt.Errorf("failed to create downloads directory: %w", err)
		}
	}
	return nil
}

func runChunkedDownload(
	ctx context.Context,
	fileURL string,
	filePath string,
	config *models.DownloadConfig,
) error {
	fileSize, err := getFileSize(ctx, fileURL, config.Timeout)
	if err != nil {
		return err
	}

	file, err := os.Create(filePath)
	if err != nil {
		return fmt.Errorf("failed to create file: %w", err)
	}
	defer file.Close()

	// pre-allocate file size if possible
	if fileSize > 0 {
		if err := file.Truncate(int64(fileSize)); err != nil {
			return fmt.Errorf("failed to allocate file space: %w", err)
		}
	}

	chunks := createChunks(fileSize, config.ChunkSize)

	semaphore := make(chan struct{}, config.Concurrency)
	var wg sync.WaitGroup

	errChan := make(chan error, 1)
	var downloadErr error
	var errOnce sync.Once

	var completedChunks int64
	var completedBytes int64
	var progressMutex sync.Mutex

	downloadCtx, cancelDownload := context.WithCancel(ctx)
	defer cancelDownload()

	for idx, chunk := range chunks {
		wg.Add(1)

		go func(idx int, chunk [2]int) {
			defer wg.Done()

			// respect concurrency limit
			select {
			case semaphore <- struct{}{}:
				defer func() { <-semaphore }()
			case <-downloadCtx.Done():
				return
			}

			chunkData, err := downloadChunkWithRetry(downloadCtx, fileURL, chunk, config)
			if err != nil {
				errOnce.Do(func() {
					downloadErr = fmt.Errorf("chunk %d: %w", idx, err)
					cancelDownload() // cancel all other downloads
					errChan <- downloadErr
				})
				return
			}

			if err := writeChunkToFile(file, chunkData, chunk[0]); err != nil {
				errOnce.Do(func() {
					downloadErr = fmt.Errorf("failed to write chunk %d: %w", idx, err)
					cancelDownload()
					errChan <- downloadErr
				})
				return
			}

			// update progress
			chunkSize := chunk[1] - chunk[0] + 1
			progressMutex.Lock()
			completedChunks++
			completedBytes += int64(chunkSize)
			progress := float64(completedBytes) / float64(fileSize)
			progressMutex.Unlock()

			// report progress if handler exists
			if config.ProgressUpdater != nil {
				config.ProgressUpdater(progress)
			}
		}(idx, chunk)
	}

	go func() {
		wg.Wait()
		close(errChan)
	}()

	select {
	case err := <-errChan:
		if err != nil {
			// clean up partial download
			os.Remove(filePath)
			return err
		}
	case <-ctx.Done():
		cancelDownload()
		os.Remove(filePath)
		return ctx.Err()
	}

	return nil
}

func getFileSize(ctx context.Context, fileURL string, timeout time.Duration) (int, error) {
	reqCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	req, err := http.NewRequestWithContext(reqCtx, http.MethodHead, fileURL, nil)
	if err != nil {
		return 0, fmt.Errorf("failed to create request: %w", err)
	}

	session := GetHTTPSession()
	resp, err := session.Do(req)
	if err != nil {
		return 0, fmt.Errorf("failed to get file size: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return 0, fmt.Errorf("failed to get file info: status code %d", resp.StatusCode)
	}

	return int(resp.ContentLength), nil
}

func downloadChunkWithRetry(
	ctx context.Context,
	fileURL string,
	chunk [2]int,
	config *models.DownloadConfig,
) ([]byte, error) {
	var lastErr error

	for attempt := 0; attempt <= config.RetryAttempts; attempt++ {
		if attempt > 0 {
			// wait before retry
			select {
			case <-ctx.Done():
				return nil, ctx.Err()
			case <-time.After(config.RetryDelay):
			}
		}

		data, err := downloadChunk(ctx, fileURL, chunk, config.Timeout)
		if err == nil {
			return data, nil
		}

		lastErr = err
	}

	return nil, fmt.Errorf("all %d attempts failed: %w", config.RetryAttempts+1, lastErr)
}

func downloadChunk(
	ctx context.Context,
	fileURL string,
	chunk [2]int,
	timeout time.Duration,
) ([]byte, error) {
	reqCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	req, err := http.NewRequestWithContext(reqCtx, http.MethodGet, fileURL, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}
	req.Header.Add("Range", fmt.Sprintf("bytes=%d-%d", chunk[0], chunk[1]))

	session := GetHTTPSession()
	resp, err := session.Do(req)
	if err != nil {
		return nil, fmt.Errorf("download failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusPartialContent && resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("unexpected status code: %d", resp.StatusCode)
	}

	return io.ReadAll(resp.Body)
}

func writeChunkToFile(file *os.File, data []byte, offset int) error {
	_, err := file.WriteAt(data, int64(offset))
	return err
}

func createChunks(fileSize int, chunkSize int) [][2]int {
	if fileSize <= 0 {
		return [][2]int{{0, 0}}
	}

	numChunks := int(math.Ceil(float64(fileSize) / float64(chunkSize)))
	chunks := make([][2]int, numChunks)

	for i := 0; i < numChunks; i++ {
		start := i * chunkSize
		end := start + chunkSize - 1
		if end >= fileSize {
			end = fileSize - 1
		}
		chunks[i] = [2]int{start, end}
	}

	return chunks
}

func DownloadSegments(
	ctx context.Context,
	segmentURLs []string,
	config *models.DownloadConfig,
) ([]string, error) {
	if config == nil {
		config = DefaultConfig()
	}

	tempDir := filepath.Join(config.DownloadDir, "segments_"+time.Now().Format("20060102_150405"))
	if err := os.MkdirAll(tempDir, 0755); err != nil {
		return nil, fmt.Errorf("failed to create temporary directory: %w", err)
	}

	semaphore := make(chan struct{}, config.Concurrency)
	var wg sync.WaitGroup

	errChan := make(chan error, len(segmentURLs))

	downloadedFiles := make([]string, len(segmentURLs))

	for i, segmentURL := range segmentURLs {
		wg.Add(1)
		go func(idx int, url string) {
			defer wg.Done()

			// acquire semaphore slot
			semaphore <- struct{}{}
			defer func() { <-semaphore }()

			segmentFileName := fmt.Sprintf("segment_%05d", idx)
			segmentPath := filepath.Join(tempDir, segmentFileName)

			_, err := DownloadFile(ctx, []string{url}, segmentFileName, &models.DownloadConfig{
				ChunkSize:       config.ChunkSize,
				Concurrency:     3, // segments are typically small
				Timeout:         config.Timeout,
				DownloadDir:     tempDir,
				RetryAttempts:   config.RetryAttempts,
				RetryDelay:      config.RetryDelay,
				Remux:           false, // don't remux individual segments
				ProgressUpdater: nil,   // no progress updates for individual segments
			})

			if err != nil {
				errChan <- fmt.Errorf("failed to download segment %d: %w", idx, err)
				return
			}

			downloadedFiles[idx] = segmentPath
		}(i, segmentURL)
	}

	go func() {
		wg.Wait()
		close(errChan)
	}()

	for err := range errChan {
		if err != nil {
			os.RemoveAll(tempDir)
			return nil, err
		}
	}

	return downloadedFiles, nil
}

func MergeSegmentFiles(
	ctx context.Context,
	segmentPaths []string,
	outputFileName string,
	config *models.DownloadConfig,
) (string, error) {
	if config == nil {
		config = DefaultConfig()
	}

	if err := ensureDownloadDir(config.DownloadDir); err != nil {
		return "", err
	}

	outputPath := filepath.Join(config.DownloadDir, outputFileName)
	outputFile, err := os.Create(outputPath)
	if err != nil {
		return "", fmt.Errorf("failed to create output file: %w", err)
	}
	defer outputFile.Close()

	var totalBytes int64
	var processedBytes int64

	if config.ProgressUpdater != nil {
		for _, segmentPath := range segmentPaths {
			fileInfo, err := os.Stat(segmentPath)
			if err == nil {
				totalBytes += fileInfo.Size()
			}
		}
	}

	for i, segmentPath := range segmentPaths {
		select {
		case <-ctx.Done():
			return "", ctx.Err()
		default:
			segmentFile, err := os.Open(segmentPath)
			if err != nil {
				return "", fmt.Errorf("failed to open segment %d: %w", i, err)
			}

			written, err := io.Copy(outputFile, segmentFile)
			segmentFile.Close()

			if err != nil {
				return "", fmt.Errorf("failed to copy segment %d: %w", i, err)
			}

			if config.ProgressUpdater != nil && totalBytes > 0 {
				processedBytes += written
				progress := float64(processedBytes) / float64(totalBytes)
				config.ProgressUpdater(progress)
			}
		}
	}

	if config.Remux {
		err := av.RemuxFile(outputPath)
		if err != nil {
			return "", fmt.Errorf("remuxing failed: %w", err)
		}
	}

	return outputPath, nil
}
