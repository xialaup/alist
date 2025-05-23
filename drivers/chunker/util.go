package chunker

import (
	"context"
	"crypto/md5"
	"encoding/hex"
	"errors"
	"fmt"
	"hash"
	"io"
	"net/http"
	"path"
	stdpath "path"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/alist-org/alist/v3/internal/conf"
	"github.com/alist-org/alist/v3/internal/model"
	"github.com/alist-org/alist/v3/internal/op"
	"github.com/alist-org/alist/v3/internal/setting"
	"github.com/alist-org/alist/v3/internal/sign"
	"github.com/alist-org/alist/v3/pkg/utils"
	"github.com/alist-org/alist/v3/server/common"
)

const optimizeFirstChunk = false

// revealHidden is a stub until chunker lands the `reveal hidden` option.
const revealHidden = false

// Prevent memory overflow due to specially crafted chunk name
const maxSafeChunkNumber = 10000000

// Number of attempts to find unique transaction identifier
const maxTransactionProbes = 100

// standard chunker errors
var (
	ErrChunkOverflow = errors.New("chunk number overflow")
	ErrMetaTooBig    = errors.New("metadata is too big")
	ErrMetaUnknown   = errors.New("unknown metadata, please upgrade rclone")
)

// variants of baseMove's parameter delMode
const (
	delNever  = 0 // don't delete, just move
	delAlways = 1 // delete destination before moving
	delFailed = 2 // move, then delete and try again if failed
)

// will give the best guessing based on the path
func guessPath(path string) (isFolder, secondTry bool) {
	if strings.HasSuffix(path, "/") {
		//confirmed a folder
		return true, false
	}
	lastSlash := strings.LastIndex(path, "/")
	if strings.Index(path[lastSlash:], ".") < 0 {
		//no dot, try folder then try file
		return true, true
	}
	return false, true
}

func (d *Chunker) getPathForRemote(path string, isFolder bool) (remoteFullPath string) {
	if isFolder && !strings.HasSuffix(path, "/") {
		path = path + "/"
	}
	dir, fileName := filepath.Split(path)

	remoteDir := dir
	remoteFileName := ""
	if len(strings.TrimSpace(fileName)) > 0 {
		remoteFileName = fileName
	}
	return stdpath.Join(d.RemotePath, remoteDir, remoteFileName)

}

// actual path is used for internal only. any link for user should come from remoteFullPath
func (d *Chunker) getActualPathForRemote(path string, isFolder bool) (string, error) {
	_, remoteActualPath, err := op.GetStorageAndActualPath(d.getPathForRemote(path, isFolder))
	return remoteActualPath, err
}

type openObject struct {
	ctx        context.Context
	mu         sync.Mutex
	apiUrl     string
	parentPath string
	storage    *Chunker
	d          []model.Obj
	args       model.LinkArgs
	id         int
	skip       int64
	chunk      *[]byte
	chunks     *[]chunkSize
	closed     bool
	sha        string
	shaTemp    hash.Hash
}

func (oo *openObject) getRawFiles(ctx context.Context, addr string) ([]byte, error) {
	fmt.Println("@@@@@@@@@@@@@@@@@@@@@@@@")
	fmt.Println(addr)
	if addr == "" {
		return nil, errors.New("addr is nil")
	}

	var rawURL string

	storage := oo.storage.remoteStorage

	query := ""
	meta, _ := op.GetNearestMeta(addr)
	if isEncrypt(meta, addr) || setting.GetBool(conf.SignAll) {
		query = "?sign=" + sign.Sign(addr)
	}

	if storage.Config().MustProxy() || storage.GetStorage().WebProxy {
		query := ""
		if isEncrypt(meta, addr) || setting.GetBool(conf.SignAll) {
			query = "?sign=" + sign.Sign(addr)
		}
		if storage.GetStorage().DownProxyUrl != "" {
			rawURL = fmt.Sprintf("%s%s?sign=%s",
				strings.Split(storage.GetStorage().DownProxyUrl, "\n")[0],
				utils.EncodePath(addr, true),
				sign.Sign(addr))
		} else {
			rawURL = fmt.Sprintf("%s/p%s%s",
				oo.apiUrl,
				utils.EncodePath(addr, true),
				query)
		}
	} else {

		rawURL = fmt.Sprintf("%s/d%s%s",
			oo.apiUrl,
			utils.EncodePath(addr, true),
			query)
	}

	client := http.Client{
		Timeout: time.Duration(60 * time.Second), // Set timeout to 5 seconds
	}
	fmt.Println(rawURL)
	resp, err := client.Get(rawURL)
	if err != nil {
		fmt.Println(err.Error())
		return nil, err
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		fmt.Println(err.Error())
		return nil, err
	}
	if resp.StatusCode != http.StatusOK {
		fmt.Println(fmt.Errorf("bad status: %s, body: %s", resp.Status, body))
		return nil, fmt.Errorf("bad status: %s, body: %s", resp.Status, body)
	}

	return body, nil
}

// get the next chunk
func (oo *openObject) getChunk(ctx context.Context) (err error) {
	if oo.id >= len(*oo.chunks) {
		return io.EOF
	}
	var chunk []byte
	err = utils.Retry(3, time.Second, func() (err error) {
		file_path := path.Join(oo.parentPath, oo.d[oo.id].GetName())
		dstDirActualPath, err := oo.storage.getActualPathForRemote(file_path, false)
		absoulte_path := path.Join(oo.storage.remoteStorage.GetStorage().MountPath, dstDirActualPath)
		chunk, err = oo.getRawFiles(oo.ctx, absoulte_path)
		return err
	})
	if err != nil {
		return err
	}
	oo.id++
	oo.chunk = &chunk
	return nil
}

// Read reads up to len(p) bytes into p.
func (oo *openObject) Read(p []byte) (n int, err error) {
	oo.mu.Lock()
	defer oo.mu.Unlock()
	if oo.closed {
		return 0, fmt.Errorf("read on closed file")
	}
	// Skip data at the start if requested
	for oo.skip > 0 {
		//size := 1024 * 1024
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
	if len(*oo.chunk) == 0 {
		err = oo.getChunk(oo.ctx)
		if err != nil {
			return 0, err
		}
		if oo.skip > 0 {
			*oo.chunk = (*oo.chunk)[oo.skip:]
			oo.skip = 0
		}
	}
	n = copy(p, *oo.chunk)
	*oo.chunk = (*oo.chunk)[n:]

	oo.shaTemp.Write(*oo.chunk)

	return n, nil
}

// Close closed the file - MAC errors are reported here
func (oo *openObject) Close() (err error) {
	oo.mu.Lock()
	defer oo.mu.Unlock()
	if oo.closed {
		return nil
	}
	// 校验Sha1
	if string(oo.shaTemp.Sum(nil)) != oo.sha {
		return fmt.Errorf("failed to finish download: %w", err)
	}

	oo.closed = true
	return nil
}

func GetMD5Hash(text string) string {
	tHash := md5.Sum([]byte(text))
	return hex.EncodeToString(tHash[:])
}

// chunkSize describes a size and position of chunk
type chunkSize struct {
	position int64
	size     int
}

func getChunkSizes(sliceSize []model.Obj) (chunks []chunkSize) {
	chunks = make([]chunkSize, 0)
	currentPos := int64(0)
	for _, chunk := range sliceSize {
		chunks = append(chunks, chunkSize{position: currentPos, size: int(chunk.GetSize())})
		currentPos += chunk.GetSize()
	}
	return chunks
}

func (oo *openObject) ChunkLocation(id int) (position int64, size int, err error) {
	if id < 0 || id >= len(*oo.chunks) {
		return 0, 0, errors.New("invalid arguments")
	}

	return (*oo.chunks)[id].position, (*oo.chunks)[id].size, nil
}

func isEncrypt(meta *model.Meta, path string) bool {
	if common.IsStorageSignEnabled(path) {
		return true
	}
	if meta == nil || meta.Password == "" {
		return false
	}
	if !utils.PathEqual(meta.Path, path) && !meta.PSub {
		return false
	}
	return true
}
