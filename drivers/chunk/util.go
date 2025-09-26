package chunk

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"sync"
	"time"

	"github.com/alist-org/alist/v3/internal/sign"
	"github.com/alist-org/alist/v3/pkg/utils"
)

// do others that not defined in Driver interface
// openObject represents a download in progress
type openObject struct {
	ctx       context.Context
	baseURL   string
	mu        sync.Mutex
	d         []string
	id        int
	skip      int64
	chunk     []byte
	chunks    *[]ChunkSize
	closed    bool
	needProxy bool
}

// get the next chunk
func (oo *openObject) getChunk(ctx context.Context) (err error) {
	if oo.id >= len(*oo.chunks) {
		return io.EOF
	}
	var chunk []byte
	err = utils.Retry(3, time.Second, func() (err error) {
		chunk, err = getRawFiles(oo.baseURL, oo.d[oo.id], oo.needProxy)
		return err
	})
	if err != nil {
		return err
	}
	oo.id++
	oo.chunk = chunk
	return nil
}

// Read reads up to len(p) bytes into p.
func (oo *openObject) Read(p []byte) (n int, err error) {
	oo.mu.Lock()
	defer oo.mu.Unlock()
	if oo.closed {
		return 0, fmt.Errorf("read on closed file")
	}
	for oo.skip > 0 {
		_, size, err := oo.ChunkLocation(oo.id)
		if err != nil {
			return 0, err
		}
		if oo.skip < int64(size) {
			break
		}
		oo.id++
		oo.skip -= int64(size)
	}
	if len(oo.chunk) == 0 {
		err = oo.getChunk(oo.ctx)
		if err != nil {
			return 0, err
		}
		if oo.skip > 0 {
			oo.chunk = oo.chunk[oo.skip:]
			oo.skip = 0
		}
	}
	n = copy(p, oo.chunk)
	oo.chunk = oo.chunk[n:]
	return n, nil
}

// Close closed the file - MAC errors are reported here
func (oo *openObject) Close() (err error) {
	oo.mu.Lock()
	defer oo.mu.Unlock()
	if oo.closed {
		return nil
	}
	err = utils.Retry(3, 500*time.Millisecond, func() (err error) {
		return nil
	})
	if err != nil {
		return fmt.Errorf("failed to finish download: %w", err)
	}
	oo.closed = true
	return nil
}
func (oo *openObject) ChunkLocation(id int) (position int64, size int, err error) {
	if id < 0 || id >= len(*oo.chunks) {
		return 0, 0, errors.New("invalid arguments")
	}

	return (*oo.chunks)[id].position, (*oo.chunks)[id].size, nil
}

// chunkSize describes a size and position of chunk
type ChunkSize struct {
	position int64
	size     int
}

func getChunkSizes(partSize int64, chunkSizes []int64) (chunks []ChunkSize) {
	p := int64(0) // Start position for the first chunk
	for _, chunk := range chunkSizes {
		if partSize <= 0 {
			break
		}
		chunkSize := chunk
		if partSize < chunk {
			chunkSize = chunk
		}
		chunks = append(chunks, ChunkSize{position: p, size: int(chunk)})
		p += int64(chunkSize)
	}
	return chunks
}

func getRawFiles(baseURL string, addr string, needProxy bool) ([]byte, error) {
	if len(addr) <= 0 {
		return nil, errors.New("没有找到文件地址，请检查配置")
	}

	url := fmt.Sprintf("%s/d%s?sign=%s",
		baseURL,
		utils.EncodePath(addr, true),
		sign.Sign(addr))

	if needProxy {
		url = fmt.Sprintf("%s/p%s?sign=%s",
			baseURL,
			utils.EncodePath(addr, true),
			sign.Sign(addr))
	}

	client := http.Client{
		Timeout: time.Duration(60 * time.Second),
	}

	resp, err := client.Get(url)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("bad status: %s, body: %s", resp.Status, body)
	}

	return body, nil
}

//方案二driver.go的Link方法：
// func (d *Chunk) Link(ctx context.Context, file model.Obj, args model.LinkArgs) (*model.Link, error) {
// 	remoteStorage, remoteActualPath, err := op.GetStorageAndActualPath(d.RemotePath)
// 	if err != nil {
// 		return nil, err
// 	}
// 	chunkFile, ok := file.(*chunkObject)
// 	remoteActualPath = stdpath.Join(remoteActualPath, file.GetPath())
// 	if !ok {
// 		return nil, errors.New("not a chunk file: " + remoteActualPath)
// 	}
// 	needProxy := false
// 	if remoteStorage.Config().MustProxy() {
// 		needProxy = true
// 	}

// 	baseURL := common.GetApiUrl(common.GetHttpReq(ctx))
// 	var fileAddrs []string
// 	for idx, _ := range chunkFile.chunkSizes {
// 		fileRemotePath := stdpath.Join(d.RemotePath, file.GetPath(), d.getPartName(idx))
// 		fileAddrs = append(fileAddrs, fileRemotePath)
// 	}
// 	size := chunkFile.GetSize()
// 	chunks := getChunkSizes(d.PartSize, chunkFile.chunkSizes)
// 	var finalClosers utils.Closers
// 	resultRangeReader := func(ctx context.Context, httpRange http_range.Range) (io.ReadCloser, error) {
// 		length := httpRange.Length
// 		if httpRange.Length >= 0 && httpRange.Start+httpRange.Length >= size {
// 			length = -1
// 		}
// 		oo := &openObject{
// 			ctx:       ctx,
// 			baseURL:   baseURL,
// 			d:         fileAddrs,
// 			chunks:    &chunks,
// 			skip:      httpRange.Start,
// 			needProxy: needProxy,
// 		}
// 		finalClosers.Add(oo)

// 		return readers.NewLimitedReadCloser(oo, length), nil
// 	}
// 	resultRangeReadCloser := &model.RangeReadCloser{RangeReader: resultRangeReader, Closers: finalClosers}
// 	resultLink := &model.Link{
// 		RangeReadCloser: resultRangeReadCloser,
// 	}
// 	return resultLink, nil
// }
