package chunker

import (
	"context"
	"crypto/sha1"
	"fmt"
	"io"
	"math"
	stdpath "path"
	"regexp"
	"strings"

	"github.com/alist-org/alist/v3/internal/driver"
	"github.com/alist-org/alist/v3/internal/errs"
	"github.com/alist-org/alist/v3/internal/fs"
	"github.com/alist-org/alist/v3/internal/model"
	"github.com/alist-org/alist/v3/internal/op"
	"github.com/alist-org/alist/v3/internal/stream"
	"github.com/alist-org/alist/v3/pkg/http_range"
	"github.com/alist-org/alist/v3/pkg/utils"
	"github.com/rclone/rclone/lib/readers"
)

type Chunker struct {
	model.Storage
	Addition
	remoteStorage driver.Driver
}

const obfuscatedPrefix = "___Obfuscated___"

func (d *Chunker) Config() driver.Config {
	return config
}

func (d *Chunker) GetAddition() driver.Additional {
	return &d.Addition
}

func (d *Chunker) Init(ctx context.Context) error {

	//need remote storage exist
	storage, err := fs.GetStorage(d.RemotePath, &fs.GetStoragesArgs{})
	if err != nil {
		return fmt.Errorf("can't find remote storage: %w", err)
	}
	d.remoteStorage = storage

	return nil
}

func (d *Chunker) Drop(ctx context.Context) error {
	return nil
}

func (d *Chunker) List(ctx context.Context, dir model.Obj, args model.ListArgs) ([]model.Obj, error) {
	path := dir.GetPath()
	objs, err := fs.List(ctx, d.getPathForRemote(path, true), &fs.ListArgs{NoLog: true})
	if err != nil {
		return nil, err
	}
	var result []model.Obj
	for _, obj := range objs {
		if obj.IsDir() {
			objRes := model.Object{
				Name:     obj.GetName(),
				Size:     0,
				Modified: obj.ModTime(),
				IsFolder: obj.IsDir(),
				Ctime:    obj.CreateTime(),
			}
			result = append(result, &objRes)
		} else {
			objRes := model.Object{
				Name:     obj.GetName(),
				Size:     obj.GetSize(),
				Modified: obj.ModTime(),
				IsFolder: obj.IsDir(),
				Ctime:    obj.CreateTime(),
			}
			result = append(result, &objRes)
		}
	}

	var fresult []model.Obj
	fileMap := make(map[string]*model.Object)
	chunkPattern := regexp.MustCompile(`^(.*)\.rclone_chunk\.\d+$`)

	for _, file := range result {
		// 检查文件是否为分片文件
		if chunkPattern.MatchString(file.GetName()) {
			// 提取主文件名
			mainFileName := chunkPattern.ReplaceAllString(file.GetName(), `$1`)

			// 如果map中已有主文件，合并分片
			if existingFile, exists := fileMap[mainFileName]; exists {
				existingFile.Size += file.GetSize()
			} else {
				// 如果是第一个分片文件，初始化主文件记录
				fileMap[mainFileName] = &model.Object{
					Name:     mainFileName,
					Size:     file.GetSize(),
					Modified: file.ModTime(),
					Ctime:    file.CreateTime(),
					IsFolder: false,
				}
			}
		} else {
			fileMap[file.GetName()] = &model.Object{
				Name:     file.GetName(),
				Size:     file.GetSize(),
				Modified: file.ModTime(),
				IsFolder: file.IsDir(),
				Ctime:    file.CreateTime(),
			}
		}
	}

	for _, f := range fileMap {
		objrefs := &model.Object{
			Name:     f.GetName(),
			Size:     f.GetSize(),
			Modified: f.ModTime(),
			IsFolder: f.IsDir(),
			Ctime:    f.CreateTime(),
		}
		fresult = append(fresult, objrefs)
	}
	return fresult, nil
}

func (d *Chunker) Get(ctx context.Context, path string) (model.Obj, error) {
	if utils.PathEqual(path, "/") {
		return &model.Object{
			Name:     "Root",
			IsFolder: true,
			Path:     "/",
		}, nil
	}

	remoteFullPath := ""
	var remoteObj model.Obj

	var err, err2 error
	firstTryIsFolder, secondTry := guessPath(path)
	remoteFullPath = d.getPathForRemote(path, firstTryIsFolder)
	remoteObj, err = fs.Get(ctx, remoteFullPath, &fs.GetArgs{NoLog: true})
	if err != nil {
		if errs.IsObjectNotFound(err) && secondTry {
			//try the opposite
			remoteFullPath = d.getPathForRemote(path, !firstTryIsFolder)
			remoteObj, err2 = fs.Get(ctx, remoteFullPath, &fs.GetArgs{NoLog: true})
			if err2 != nil {
				return nil, err2
			}
		} else {
			return nil, err
		}
	}
	var size int64 = 0
	name := ""
	if !remoteObj.IsDir() {
		name = remoteObj.GetName()
		parentPath, _ := stdpath.Split(path)
		var result []model.Obj
		result, err := op.List(ctx, d, parentPath, model.ListArgs{})
		if err != nil {
			return nil, err
		}
		for _, file := range result {
			if file.GetName() == name {
				size = file.GetSize()
				break
			}
		}

	} else {
		name = remoteObj.GetName()
	}
	obj := &model.Object{
		Path:     path,
		Name:     name,
		Size:     size,
		Modified: remoteObj.ModTime(),
		IsFolder: remoteObj.IsDir(),
	}
	return obj, nil
	//return nil, errs.ObjectNotFound
}

func (d *Chunker) Link(ctx context.Context, file model.Obj, args model.LinkArgs) (*model.Link, error) {
	chunkSize := int64(1024 * 1024 * d.ChunkSize)
	file_size := file.GetSize()
	chunkCount := int(math.Ceil(float64(file_size) / float64(chunkSize)))

	if chunkCount > 1 {
		parentPath, _ := stdpath.Split(file.GetPath())
		objs, err := fs.List(ctx, d.getPathForRemote(parentPath, true), &fs.ListArgs{NoLog: true})
		if err != nil {
			return nil, err
		}

		var result []model.Obj
		for _, obj := range objs {
			if obj.IsDir() {
				continue
			} else {
				if strings.HasPrefix(obj.GetPath(), file.GetPath()+".rclone_chunk") {
					objRes := model.Object{
						Name:     obj.GetName(),
						Size:     obj.GetSize(),
						Modified: obj.ModTime(),
						IsFolder: obj.IsDir(),
						Ctime:    obj.CreateTime(),
					}
					result = append(result, &objRes)
				} else {
					continue
				}
			}
		}
		result = append(result, file)
		model.SortFiles(result, "name", "asc")
		chunks := getChunkSizes(result)

		var remoteClosers utils.Closers
		resultRangeReader := func(ctx context.Context, httpRange http_range.Range) (io.ReadCloser, error) {
			length := httpRange.Length
			underlyingOffset := httpRange.Start
			underlyingLength := httpRange.Length
			if underlyingLength >= 0 && underlyingOffset+underlyingLength >= file_size {
				length = -1
			}

			oo := &openObject{
				ctx:        ctx,
				d:          result,
				parentPath: parentPath,
				storage:    d,
				chunk:      &[]byte{},
				chunks:     &chunks,
				skip:       httpRange.Start,
				sha:        "",
				shaTemp:    sha1.New(),
				args:       args,
			}
			remoteClosers.Add(oo)
			return readers.NewLimitedReadCloser(oo, length), nil

		}

		resultRangeReadCloser := &model.RangeReadCloser{RangeReader: resultRangeReader, Closers: remoteClosers}
		return &model.Link{
			RangeReadCloser: resultRangeReadCloser,
		}, nil

	} else {

		dstDirActualPath, err := d.getActualPathForRemote(file.GetPath(), false)
		if err != nil {
			return nil, fmt.Errorf("failed to convert path to remote path: %w", err)
		}
		remoteLink, remoteFile, err := op.Link(ctx, d.remoteStorage, dstDirActualPath, args)
		if err != nil {
			return nil, err
		}
		if remoteLink.RangeReadCloser == nil && remoteLink.MFile == nil && len(remoteLink.URL) == 0 {
			return nil, fmt.Errorf("the remote storage driver need to be enhanced to support encrytion")
		}

		remoteFileSize := remoteFile.GetSize()
		remoteClosers := utils.EmptyClosers()
		resultRangeReader := func(ctx context.Context, httpRange http_range.Range) (io.ReadCloser, error) {
			length := httpRange.Length
			underlyingOffset := httpRange.Start
			underlyingLength := httpRange.Length

			if underlyingLength >= 0 && underlyingOffset+underlyingLength >= remoteFileSize {
				length = -1
			}
			rrc := remoteLink.RangeReadCloser
			if len(remoteLink.URL) > 0 {

				rangedRemoteLink := &model.Link{
					URL:    remoteLink.URL,
					Header: remoteLink.Header,
				}
				var converted, err = stream.GetRangeReadCloserFromLink(remoteFileSize, rangedRemoteLink)
				if err != nil {
					return nil, err
				}
				rrc = converted
			}
			if rrc != nil {
				remoteReader, err := rrc.RangeRead(ctx, http_range.Range{Start: underlyingOffset, Length: length})
				remoteClosers.AddClosers(rrc.GetClosers())
				if err != nil {
					return nil, err
				}
				return remoteReader, nil
			}
			if remoteLink.MFile != nil {
				_, err := remoteLink.MFile.Seek(underlyingOffset, io.SeekStart)
				if err != nil {
					return nil, err
				}
				remoteClosers.Add(remoteLink.MFile)
				return io.NopCloser(remoteLink.MFile), nil
			}
			return nil, errs.NotSupport
		}

		resultRangeReadCloser := &model.RangeReadCloser{RangeReader: resultRangeReader, Closers: remoteClosers}
		resultLink := &model.Link{
			Header:          remoteLink.Header,
			RangeReadCloser: resultRangeReadCloser,
			Expiration:      remoteLink.Expiration,
		}

		return resultLink, nil
	}

}

func (d *Chunker) MakeDir(ctx context.Context, parentDir model.Obj, dirName string) error {
	dstDirActualPath, err := d.getActualPathForRemote(parentDir.GetPath(), true)
	if err != nil {
		return fmt.Errorf("failed to convert path to remote path: %w", err)
	}
	dir := dirName
	return op.MakeDir(ctx, d.remoteStorage, stdpath.Join(dstDirActualPath, dir))
}

func (d *Chunker) Move(ctx context.Context, srcObj, dstDir model.Obj) error {
	srcRemoteActualPath, err := d.getActualPathForRemote(srcObj.GetPath(), srcObj.IsDir())
	if err != nil {
		return fmt.Errorf("failed to convert path to remote path: %w", err)
	}
	dstRemoteActualPath, err := d.getActualPathForRemote(dstDir.GetPath(), dstDir.IsDir())
	if err != nil {
		return fmt.Errorf("failed to convert path to remote path: %w", err)
	}
	return op.Move(ctx, d.remoteStorage, srcRemoteActualPath, dstRemoteActualPath)
}

func (d *Chunker) Rename(ctx context.Context, srcObj model.Obj, newName string) error {
	remoteActualPath, err := d.getActualPathForRemote(srcObj.GetPath(), srcObj.IsDir())
	if err != nil {
		return fmt.Errorf("failed to convert path to remote path: %w", err)
	}
	var newEncryptedName string
	if srcObj.IsDir() {
		newEncryptedName = newName
	} else {
		newEncryptedName = newName
	}
	return op.Rename(ctx, d.remoteStorage, remoteActualPath, newEncryptedName)
}

func (d *Chunker) Copy(ctx context.Context, srcObj, dstDir model.Obj) error {
	srcRemoteActualPath, err := d.getActualPathForRemote(srcObj.GetPath(), srcObj.IsDir())
	if err != nil {
		return fmt.Errorf("failed to convert path to remote path: %w", err)
	}
	dstRemoteActualPath, err := d.getActualPathForRemote(dstDir.GetPath(), dstDir.IsDir())
	if err != nil {
		return fmt.Errorf("failed to convert path to remote path: %w", err)
	}
	return op.Copy(ctx, d.remoteStorage, srcRemoteActualPath, dstRemoteActualPath)

}

func (d *Chunker) Remove(ctx context.Context, obj model.Obj) error {
	remoteActualPath, err := d.getActualPathForRemote(obj.GetPath(), obj.IsDir())
	if err != nil {
		return fmt.Errorf("failed to convert path to remote path: %w", err)
	}
	return op.Remove(ctx, d.remoteStorage, remoteActualPath)
}

func (d *Chunker) Put(ctx context.Context, dstDir model.Obj, streamer model.FileStreamer, up driver.UpdateProgress) error {
	dstDirActualPath, err := d.getActualPathForRemote(dstDir.GetPath(), true)
	if err != nil {
		return fmt.Errorf("failed to convert path to remote path: %w", err)
	}

	// doesn't support seekableStream, since rapid-upload is not working for encrypted data
	streamOut := &stream.FileStream{
		Obj: &model.Object{
			ID:       streamer.GetID(),
			Path:     streamer.GetPath(),
			Name:     streamer.GetName(),
			Size:     streamer.GetSize(),
			Modified: streamer.ModTime(),
			IsFolder: streamer.IsDir(),
		},
		Reader:            streamer,
		Mimetype:          "application/octet-stream",
		WebPutAsTask:      streamer.NeedStore(),
		ForceStreamUpload: true,
		Exist:             streamer.GetExist(),
	}
	err = op.Put(ctx, d.remoteStorage, dstDirActualPath, streamOut, up, false)
	if err != nil {
		return err
	}
	return nil
}

//func (d *Safe) Other(ctx context.Context, args model.OtherArgs) (interface{}, error) {
//	return nil, errs.NotSupport
//}

var _ driver.Driver = (*Chunker)(nil)
