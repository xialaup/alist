package chunk

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	stdpath "path"
	"strconv"
	"strings"

	"github.com/alist-org/alist/v3/internal/driver"
	"github.com/alist-org/alist/v3/internal/fs"
	"github.com/alist-org/alist/v3/internal/model"
	"github.com/alist-org/alist/v3/internal/op"
	"github.com/alist-org/alist/v3/internal/sign"
	"github.com/alist-org/alist/v3/internal/stream"
	"github.com/alist-org/alist/v3/pkg/http_range"
	"github.com/alist-org/alist/v3/pkg/utils"
	"github.com/alist-org/alist/v3/server/common"
)

type Chunk struct {
	model.Storage
	Addition
	rcloneFormat *rcloneNameFormat
}

func (d *Chunk) Config() driver.Config {
	return config
}

func (d *Chunk) GetAddition() driver.Additional {
	return &d.Addition
}

func (d *Chunk) Init(ctx context.Context) error {
	if d.PartSize <= 0 {
		return errors.New("part size must be positive")
	}
	rclonePattern := d.RcloneNameFormat
	if rclonePattern == "" {
		rclonePattern = rcloneDefaultNameFormat
	}
	rcloneFormat, err := newRcloneNameFormat(rclonePattern, d.RcloneStartFrom)
	if err != nil {
		return err
	}
	d.rcloneFormat = rcloneFormat
	if d.UploadNameFormat == "" {
		d.UploadNameFormat = "openlist"
	}
	if d.UploadNameFormat != "openlist" && d.UploadNameFormat != "rclone" {
		return fmt.Errorf("unsupported upload_name_format: %s", d.UploadNameFormat)
	}
	d.RemotePath = utils.FixAndCleanPath(d.RemotePath)
	return nil
}

func (d *Chunk) Drop(ctx context.Context) error {
	return nil
}

func (d *Chunk) Get(ctx context.Context, path string) (model.Obj, error) {
	if utils.PathEqual(path, "/") {
		return &model.Object{
			Name:     "Root",
			IsFolder: true,
			Path:     "/",
		}, nil
	}
	remoteStorage, remoteActualPath, err := op.GetStorageAndActualPath(d.RemotePath)
	if err != nil {
		return nil, err
	}
	remoteActualPath = stdpath.Join(remoteActualPath, path)
	if remoteObj, err := op.Get(ctx, remoteStorage, remoteActualPath); err == nil {
		if !remoteObj.IsDir() && remoteObj.GetSize() <= rcloneMaxMetadataSize {
			remoteActualDir, _ := stdpath.Split(remoteActualPath)
			entries, listErr := op.List(ctx, remoteStorage, remoteActualDir, model.ListArgs{})
			if listErr == nil {
				if obj, metaErr := buildRcloneObject(ctx, remoteStorage, d.rcloneFormat, remoteActualPath, path, remoteObj, entries); metaErr == nil {
					return obj, nil
				}
			}
		}
		return &model.Object{
			Path:     path,
			Name:     remoteObj.GetName(),
			Size:     remoteObj.GetSize(),
			Modified: remoteObj.ModTime(),
			IsFolder: remoteObj.IsDir(),
			HashInfo: remoteObj.GetHash(),
		}, nil
	}

	remoteActualDir, name := stdpath.Split(remoteActualPath)
	chunkName := "[openlist_chunk]" + name
	chunkObjs, err := op.List(ctx, remoteStorage, stdpath.Join(remoteActualDir, chunkName), model.ListArgs{})
	if err != nil {
		return nil, err
	}
	var totalSize int64 = 0
	// 0号块必须存在
	chunkSizes := []int64{-1}
	h := make(map[*utils.HashType]string)
	var first model.Obj
	for _, o := range chunkObjs {
		if o.IsDir() {
			continue
		}
		if after, ok := strings.CutPrefix(o.GetName(), "hash_"); ok {
			hn, value, ok := strings.Cut(strings.TrimSuffix(after, d.CustomExt), "_")
			if ok {
				ht, ok := utils.GetHashByName(hn)
				if ok {
					h[ht] = value
				}
			}
			continue
		}
		idx, err := strconv.Atoi(strings.TrimSuffix(o.GetName(), d.CustomExt))
		if err != nil {
			continue
		}
		totalSize += o.GetSize()
		if len(chunkSizes) > idx {
			if idx == 0 {
				first = o
			}
			chunkSizes[idx] = o.GetSize()
		} else if len(chunkSizes) == idx {
			chunkSizes = append(chunkSizes, o.GetSize())
		} else {
			newChunkSizes := make([]int64, idx+1)
			copy(newChunkSizes, chunkSizes)
			chunkSizes = newChunkSizes
			chunkSizes[idx] = o.GetSize()
		}
	}
	// 检查0号块不等于-1 以支持空文件
	// 如果块数量大于1 最后一块不可能为0
	// 只检查中间块是否有0
	for i, l := 0, len(chunkSizes)-2; ; i++ {
		if i == 0 {
			if chunkSizes[i] == -1 {
				return nil, fmt.Errorf("chunk part[%d] are missing", i)
			}
		} else if chunkSizes[i] == 0 {
			return nil, fmt.Errorf("chunk part[%d] are missing", i)
		}
		if i >= l {
			break
		}
	}
	reqDir, _ := stdpath.Split(path)
	objRes := chunkObject{
		Object: model.Object{
			Path:     stdpath.Join(reqDir, chunkName),
			Name:     name,
			Size:     totalSize,
			Modified: first.ModTime(),
			Ctime:    first.CreateTime(),
		},
		chunkSizes: chunkSizes,
		partDir:    stdpath.Join(reqDir, chunkName),
	}
	if len(h) > 0 {
		objRes.HashInfo = utils.NewHashInfoByMap(h)
	}
	return &objRes, nil
}

func (d *Chunk) List(ctx context.Context, dir model.Obj, args model.ListArgs) ([]model.Obj, error) {
	remoteStorage, remoteActualPath, err := op.GetStorageAndActualPath(d.RemotePath)
	if err != nil {
		return nil, err
	}
	remoteActualDir := stdpath.Join(remoteActualPath, dir.GetPath())
	remoteObjs, err := op.List(ctx, remoteStorage, remoteActualDir, model.ListArgs{
		ReqPath: args.ReqPath,
		Refresh: args.Refresh,
	})
	if err != nil {
		return nil, err
	}
	rcloneObjects := make(map[string]*chunkObject)
	rcloneChunkNames := make(map[string]struct{})
	for _, obj := range remoteObjs {
		if obj.IsDir() || obj.GetSize() > rcloneMaxMetadataSize {
			continue
		}
		rawName := obj.GetName()
		chunkObj, err := buildRcloneObject(ctx, remoteStorage, d.rcloneFormat, stdpath.Join(remoteActualDir, rawName), stdpath.Join(dir.GetPath(), rawName), obj, remoteObjs)
		if err != nil {
			continue
		}
		rcloneObjects[rawName] = chunkObj
		for idx := range chunkObj.chunkSizes {
			rcloneChunkNames[d.rcloneFormat.partName(rawName, idx, chunkObj.rcloneXactID)] = struct{}{}
		}
	}
	result := make([]model.Obj, 0, len(remoteObjs))
	for _, obj := range remoteObjs {
		rawName := obj.GetName()
		if _, ok := rcloneChunkNames[rawName]; ok {
			continue
		}
		if obj.IsDir() {
			if name, ok := strings.CutPrefix(rawName, "[openlist_chunk]"); ok {
				chunkObjs, err := op.List(ctx, remoteStorage, stdpath.Join(remoteActualDir, rawName), model.ListArgs{
					ReqPath: stdpath.Join(args.ReqPath, rawName),
					Refresh: args.Refresh,
				})
				if err != nil {
					return nil, err
				}
				totalSize := int64(0)
				h := make(map[*utils.HashType]string)
				first := obj
				for _, o := range chunkObjs {
					if o.IsDir() {
						continue
					}
					if after, ok := strings.CutPrefix(strings.TrimSuffix(o.GetName(), d.CustomExt), "hash_"); ok {
						hn, value, ok := strings.Cut(after, "_")
						if ok {
							ht, ok := utils.GetHashByName(hn)
							if ok {
								h[ht] = value
							}
							continue
						}
					}
					idx, err := strconv.Atoi(strings.TrimSuffix(o.GetName(), d.CustomExt))
					if err != nil {
						continue
					}
					if idx == 0 {
						first = o
					}
					totalSize += o.GetSize()
				}
				objRes := model.Object{
					Name:     name,
					Size:     totalSize,
					Modified: first.ModTime(),
					Ctime:    first.CreateTime(),
				}
				if len(h) > 0 {
					objRes.HashInfo = utils.NewHashInfoByMap(h)
				}
				if !d.Thumbnail {
					result = append(result, &objRes)
				} else {
					thumbPath := stdpath.Join(args.ReqPath, ".thumbnails", name+".webp")
					thumb := fmt.Sprintf("%s/d%s?sign=%s",
						common.GetApiUrl(common.GetHttpReq(ctx)),
						utils.EncodePath(thumbPath, true),
						sign.Sign(thumbPath))
					result = append(result, &model.ObjThumb{
						Object: objRes,
						Thumbnail: model.Thumbnail{
							Thumbnail: thumb,
						},
					})
				}
				continue
			}
		}

		if chunkObj, ok := rcloneObjects[rawName]; ok {
			objRes := chunkObj.Object
			if !d.Thumbnail {
				result = append(result, &objRes)
			} else {
				thumbPath := stdpath.Join(args.ReqPath, ".thumbnails", rawName+".webp")
				thumb := fmt.Sprintf("%s/d%s?sign=%s",
					common.GetApiUrl(common.GetHttpReq(ctx)),
					utils.EncodePath(thumbPath, true),
					sign.Sign(thumbPath))
				result = append(result, &model.ObjThumb{
					Object: objRes,
					Thumbnail: model.Thumbnail{
						Thumbnail: thumb,
					},
				})
			}
			continue
		}

		if !d.ShowHidden && strings.HasPrefix(rawName, ".") {
			continue
		}
		thumb, ok := model.GetThumb(obj)
		objRes := model.Object{
			Name:     rawName,
			Size:     obj.GetSize(),
			Modified: obj.ModTime(),
			IsFolder: obj.IsDir(),
			HashInfo: obj.GetHash(),
		}
		if !ok {
			result = append(result, &objRes)
		} else {
			result = append(result, &model.ObjThumb{
				Object: objRes,
				Thumbnail: model.Thumbnail{
					Thumbnail: thumb,
				},
			})
		}
	}
	return result, nil
}

func (d *Chunk) Link(ctx context.Context, file model.Obj, args model.LinkArgs) (*model.Link, error) {
	remoteStorage, remoteActualPath, err := op.GetStorageAndActualPath(d.RemotePath)
	if err != nil {
		return nil, err
	}
	chunkFile, ok := file.(*chunkObject)
	if !ok {
		return nil, errors.New("not a chunk file: " + remoteActualPath)
	}
	if chunkFile.partDir != "" {
		remoteActualPath = stdpath.Join(remoteActualPath, chunkFile.partDir)
	} else {
		remoteActualPath = stdpath.Join(remoteActualPath, file.GetPath())
	}
	fileSize := chunkFile.GetSize()
	partName := func(idx int) string {
		if chunkFile.rclone {
			return d.rcloneFormat.partName(file.GetName(), idx, chunkFile.rcloneXactID)
		}
		return d.getPartName(idx)
	}
	var finalClosers utils.Closers
	openPart := func(ctx context.Context, idx int, start, length int64) (io.ReadCloser, error) {
		l, _, err := op.Link(ctx, remoteStorage, stdpath.Join(remoteActualPath, partName(idx)), args)
		if err != nil {
			return nil, err
		}
		if l == nil {
			return nil, fmt.Errorf("chunk part[%d] link is nil", idx)
		}
		rrc := l.RangeReadCloser
		if len(l.URL) > 0 {
			rangedRemoteLink := &model.Link{
				URL:    l.URL,
				Header: l.Header,
			}
			converted, err := stream.GetRangeReadCloserFromLink(chunkFile.chunkSizes[idx], rangedRemoteLink)
			if err != nil {
				return nil, err
			}
			rrc = converted
		}
		if rrc != nil {
			remoteReader, err := rrc.RangeRead(ctx, http_range.Range{Start: start, Length: length})
			finalClosers.AddClosers(rrc.GetClosers())
			if err != nil {
				return nil, err
			}
			finalClosers.Add(remoteReader)
			return remoteReader, nil
		}
		if l.MFile != nil {
			_, err := l.MFile.Seek(start, io.SeekStart)
			if err != nil {
				return nil, err
			}
			finalClosers.Add(l.MFile)
			if length >= 0 {
				return io.NopCloser(io.LimitReader(l.MFile, length)), nil
			}
			return io.NopCloser(l.MFile), nil
		}
		return nil, fmt.Errorf("chunk part[%d] has no readable link", idx)
	}
	resultRangeReader := func(ctx context.Context, httpRange http_range.Range) (io.ReadCloser, error) {
		start := httpRange.Start
		length := httpRange.Length
		if length < 0 || start+length > fileSize {
			length = fileSize - start
		}
		if length == 0 {
			return io.NopCloser(strings.NewReader("")), nil
		}
		remaining := length
		readers := make([]io.Reader, 0)
		for idx, chunkSize := range chunkFile.chunkSizes {
			if newStart := start - chunkSize; newStart >= 0 {
				start = newStart
			} else {
				readLength := chunkSize - start
				if remaining >= 0 && readLength > remaining {
					readLength = remaining
				}
				reader, err := openPart(ctx, idx, start, readLength)
				if err != nil {
					return nil, err
				}
				readers = append(readers, reader)
				if remaining >= 0 {
					remaining -= readLength
					if remaining <= 0 {
						break
					}
				}
				start = 0
			}
		}
		if len(readers) == 0 || remaining > 0 {
			return nil, fmt.Errorf("invalid range: start=%d,length=%d,fileSize=%d", httpRange.Start, httpRange.Length, fileSize)
		}
		return io.NopCloser(io.MultiReader(readers...)), nil
	}
	resultRangeReadCloser := &model.RangeReadCloser{RangeReader: resultRangeReader, Closers: finalClosers}
	resultLink := &model.Link{
		RangeReadCloser: resultRangeReadCloser,
	}
	return resultLink, nil
}

func (d *Chunk) MakeDir(ctx context.Context, parentDir model.Obj, dirName string) error {
	path := stdpath.Join(d.RemotePath, parentDir.GetPath(), dirName)
	return fs.MakeDir(ctx, path)
}

func (d *Chunk) Move(ctx context.Context, srcObj, dstDir model.Obj) error {
	src := stdpath.Join(d.RemotePath, srcObj.GetPath())
	dst := stdpath.Join(d.RemotePath, dstDir.GetPath())
	err := fs.Move(ctx, src, dst)
	return err
}

func (d *Chunk) Rename(ctx context.Context, srcObj model.Obj, newName string) error {
	if _, ok := srcObj.(*chunkObject); ok {
		newName = "[openlist_chunk]" + newName
	}
	return fs.Rename(ctx, stdpath.Join(d.RemotePath, srcObj.GetPath()), newName)
}

func (d *Chunk) Copy(ctx context.Context, srcObj, dstDir model.Obj) error {
	dst := stdpath.Join(d.RemotePath, dstDir.GetPath())
	src := stdpath.Join(d.RemotePath, srcObj.GetPath())
	_, err := fs.Copy(ctx, src, dst, false)
	return err
}

func (d *Chunk) Remove(ctx context.Context, obj model.Obj) error {
	return fs.Remove(ctx, stdpath.Join(d.RemotePath, obj.GetPath()))
}

func (d *Chunk) Put(ctx context.Context, dstDir model.Obj, file model.FileStreamer, up driver.UpdateProgress) error {
	remoteStorage, remoteActualPath, err := op.GetStorageAndActualPath(d.RemotePath)
	if err != nil {
		return err
	}
	if d.Thumbnail && dstDir.GetName() == ".thumbnails" {
		return op.Put(ctx, remoteStorage, stdpath.Join(remoteActualPath, dstDir.GetPath()), file, up)
	}
	upReader := &driver.ReaderUpdatingProgress{
		Reader:         file,
		UpdateProgress: up,
	}
	if d.UploadNameFormat == "rclone" {
		return d.putRclone(ctx, remoteStorage, remoteActualPath, dstDir, file, upReader)
	}
	return d.putOpenList(ctx, remoteStorage, remoteActualPath, dstDir, file, upReader)
}

func (d *Chunk) putOpenList(ctx context.Context, storage driver.Driver, rootPath string, dstDir model.Obj, file model.FileStreamer, upReader io.Reader) error {
	dst := stdpath.Join(rootPath, dstDir.GetPath(), "[openlist_chunk]"+file.GetName())
	if d.StoreHash {
		for ht, value := range file.GetHash().All() {
			_ = op.Put(ctx, storage, dst, &stream.FileStream{
				Obj: &model.Object{
					Name:     fmt.Sprintf("hash_%s_%s%s", ht.Name, value, d.CustomExt),
					Size:     1,
					Modified: file.ModTime(),
				},
				Mimetype: "application/octet-stream",
				Reader:   bytes.NewReader([]byte{0}), // 兼容不支持空文件的驱动
			}, nil, true)
		}
	}
	fullPartCount := int(file.GetSize() / d.PartSize)
	tailSize := file.GetSize() % d.PartSize
	if tailSize == 0 && fullPartCount > 0 {
		fullPartCount--
		tailSize = d.PartSize
	}
	partIndex := 0
	for partIndex < fullPartCount {
		err := op.Put(ctx, storage, dst, &stream.FileStream{
			Obj: &model.Object{
				Name:     d.getPartName(partIndex),
				Size:     d.PartSize,
				Modified: file.ModTime(),
			},
			Mimetype: file.GetMimetype(),
			Reader:   io.LimitReader(upReader, d.PartSize),
		}, nil, true)
		if err != nil {
			_ = op.Remove(ctx, storage, dst)
			return err
		}
		partIndex++
	}
	err := op.Put(ctx, storage, dst, &stream.FileStream{
		Obj: &model.Object{
			Name:     d.getPartName(fullPartCount),
			Size:     tailSize,
			Modified: file.ModTime(),
		},
		Mimetype: file.GetMimetype(),
		Reader:   upReader,
	}, nil)
	if err != nil {
		_ = op.Remove(ctx, storage, dst)
	}
	return err
}

func (d *Chunk) getPartName(part int) string {
	return fmt.Sprintf("%d%s", part, d.CustomExt)
}

var _ driver.Driver = (*Chunk)(nil)
