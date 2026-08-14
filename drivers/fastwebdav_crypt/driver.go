package fastwebdav_crypt

import (
	"context"
	"fmt"
	"io"
	stdpath "path"
	"strings"

	"github.com/alist-org/alist/v3/internal/driver"
	"github.com/alist-org/alist/v3/internal/errs"
	"github.com/alist-org/alist/v3/internal/fs"
	"github.com/alist-org/alist/v3/internal/model"
	"github.com/alist-org/alist/v3/internal/stream"
	"github.com/alist-org/alist/v3/pkg/http_range"
	"github.com/alist-org/alist/v3/pkg/utils"
)

type FastWebdavCrypt struct {
	model.Storage
	Addition
	remoteStorage driver.Driver
	encryptedDirs []encryptedDirRule
}

type encryptedDirRule struct {
	path     string
	password string
}

func (d *FastWebdavCrypt) Config() driver.Config {
	return config
}

func (d *FastWebdavCrypt) GetAddition() driver.Additional {
	return &d.Addition
}

func (d *FastWebdavCrypt) Init(ctx context.Context) error {
	d.RemotePath = utils.FixAndCleanPath(d.RemotePath)
	d.encryptedDirs = d.parseEncryptedDirs()
	storage, err := fs.GetStorage(d.RemotePath, &fs.GetStoragesArgs{})
	if err != nil {
		return fmt.Errorf("can't find remote storage: %w", err)
	}
	d.remoteStorage = storage
	return nil
}

func (d *FastWebdavCrypt) Drop(ctx context.Context) error {
	d.remoteStorage = nil
	return nil
}

func (d *FastWebdavCrypt) List(ctx context.Context, dir model.Obj, args model.ListArgs) ([]model.Obj, error) {
	remotePath := dir.GetID()
	if remotePath == "" || utils.PathEqual(dir.GetPath(), "/") {
		remotePath = d.RemotePath
	}
	dirEncrypted, password := d.encryptionForDir(dir.GetPath())
	objs, err := fs.List(ctx, remotePath, &fs.ListArgs{NoLog: true, Refresh: args.Refresh})
	if err != nil {
		return nil, err
	}
	res := make([]model.Obj, 0, len(objs))
	for _, obj := range objs {
		name := obj.GetName()
		if dirEncrypted && !obj.IsDir() {
			decoded, err := d.decodeFileName(name, password)
			if err != nil {
				continue
			}
			name = decoded
		}
		if !d.ShowHidden && strings.HasPrefix(name, ".") {
			continue
		}
		item := model.Object{
			ID:       stdpath.Join(remotePath, obj.GetName()),
			Name:     name,
			Size:     obj.GetSize(),
			Modified: obj.ModTime(),
			Ctime:    obj.CreateTime(),
			IsFolder: obj.IsDir(),
		}
		if thumb, ok := model.GetThumb(obj); ok {
			res = append(res, &model.ObjThumb{
				Object: item,
				Thumbnail: model.Thumbnail{
					Thumbnail: thumb,
				},
			})
			continue
		}
		res = append(res, &item)
	}
	return res, nil
}

func (d *FastWebdavCrypt) Get(ctx context.Context, path string) (model.Obj, error) {
	path = utils.FixAndCleanPath(path)
	if utils.PathEqual(path, "/") {
		return &model.Object{
			ID:       d.RemotePath,
			Path:     "/",
			Name:     "Root",
			Modified: d.Modified,
			IsFolder: true,
		}, nil
	}
	parts := splitPath(path)
	remotePath := d.RemotePath
	var current model.Obj
	for i, part := range parts {
		parentDisplayPath := "/" + strings.Join(parts[:i], "/")
		parentEncrypted, password := d.encryptionForDir(parentDisplayPath)
		objs, err := fs.List(ctx, remotePath, &fs.ListArgs{NoLog: true})
		if err != nil {
			return nil, err
		}
		last := i == len(parts)-1
		current = nil
		for _, obj := range objs {
			name := obj.GetName()
			if parentEncrypted && !obj.IsDir() {
				decoded, err := d.decodeFileName(name, password)
				if err != nil {
					continue
				}
				name = decoded
			}
			if name != part {
				continue
			}
			fullRemotePath := stdpath.Join(remotePath, obj.GetName())
			current = &model.Object{
				ID:       fullRemotePath,
				Path:     "/" + strings.Join(parts[:i+1], "/"),
				Name:     name,
				Size:     obj.GetSize(),
				Modified: obj.ModTime(),
				Ctime:    obj.CreateTime(),
				IsFolder: obj.IsDir(),
			}
			remotePath = fullRemotePath
			break
		}
		if current == nil {
			return nil, errs.ObjectNotFound
		}
		if !last && !current.IsDir() {
			return nil, errs.ObjectNotFound
		}
	}
	return current, nil
}

func (d *FastWebdavCrypt) Link(ctx context.Context, file model.Obj, args model.LinkArgs) (*model.Link, error) {
	remotePath := file.GetID()
	if remotePath == "" {
		obj, err := d.Get(ctx, file.GetPath())
		if err != nil {
			return nil, err
		}
		remotePath = obj.GetID()
	}
	remoteLink, remoteFile, err := fs.Link(ctx, remotePath, args)
	if err != nil {
		return nil, err
	}
	encrypted, password := d.encryptionForDir(objParentPath(file))
	if !encrypted {
		return remoteLink, nil
	}
	remoteFileSize := remoteFile.GetSize()
	remoteClosers := utils.EmptyClosers()
	rangeReader := func(ctx context.Context, r http_range.Range) (io.ReadCloser, error) {
		length := r.Length
		if length >= 0 && r.Start+length >= remoteFileSize {
			length = -1
		}
		rrc := remoteLink.RangeReadCloser
		if remoteLink.URL != "" {
			converted, err := stream.GetRangeReadCloserFromLink(remoteFileSize, &model.Link{
				URL:         remoteLink.URL,
				Header:      remoteLink.Header,
				Concurrency: remoteLink.Concurrency,
				PartSize:    remoteLink.PartSize,
			})
			if err != nil {
				return nil, err
			}
			rrc = converted
		}
		if rrc != nil {
			rc, err := rrc.RangeRead(ctx, http_range.Range{Start: r.Start, Length: length})
			remoteClosers.AddClosers(rrc.GetClosers())
			if err != nil {
				return nil, err
			}
			return decryptReader(password, remoteFileSize, r.Start, rc), nil
		}
		if remoteLink.MFile != nil {
			_, err := remoteLink.MFile.Seek(r.Start, io.SeekStart)
			if err != nil {
				return nil, err
			}
			remoteClosers.Add(remoteLink.MFile)
			var reader io.Reader = remoteLink.MFile
			if length >= 0 {
				reader = io.LimitReader(reader, length)
			}
			return decryptReader(password, remoteFileSize, r.Start, io.NopCloser(reader)), nil
		}
		return nil, errs.NotSupport
	}
	return &model.Link{
		Header:          remoteLink.Header,
		RangeReadCloser: &model.RangeReadCloser{RangeReader: rangeReader, Closers: remoteClosers},
		Expiration:      remoteLink.Expiration,
	}, nil
}

func (d *FastWebdavCrypt) decodeFileName(name string, password string) (string, error) {
	candidates := []string{name}
	if idx := strings.LastIndex(name, "."); idx > 0 {
		candidates = append([]string{name[:idx-1], name[:idx]}, candidates...)
	}
	var lastErr error
	for _, encoded := range candidates {
		if encoded == "" {
			continue
		}
		decoded, err := mixBase64Decode(aesPasswordOutward(password), encoded)
		if err == nil {
			return decoded, nil
		}
		lastErr = err
	}
	if lastErr != nil {
		return "", lastErr
	}
	return "", fmt.Errorf("empty encoded file name")
}

func (d *FastWebdavCrypt) parseEncryptedDirs() []encryptedDirRule {
	lines := strings.Split(d.EncryptedDirs, "\n")
	rules := make([]encryptedDirRule, 0, len(lines))
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		password := d.Password
		if p, v, ok := strings.Cut(line, "="); ok {
			line = strings.TrimSpace(p)
			if v = strings.TrimSpace(v); v != "" {
				password = v
			}
		}
		if line == "" {
			continue
		}
		rules = append(rules, encryptedDirRule{
			path:     utils.FixAndCleanPath(line),
			password: password,
		})
	}
	return rules
}

func (d *FastWebdavCrypt) encryptionForDir(dirPath string) (bool, string) {
	dirPath = utils.FixAndCleanPath(dirPath)
	encrypted := d.RemotePathEncrypted
	password := d.Password
	bestLen := -1
	for _, rule := range d.encryptedDirs {
		if !pathHasPrefix(dirPath, rule.path) || len(rule.path) <= bestLen {
			continue
		}
		bestLen = len(rule.path)
		encrypted = true
		password = rule.password
	}
	return encrypted, password
}

func pathHasPrefix(path, prefix string) bool {
	path = utils.FixAndCleanPath(path)
	prefix = utils.FixAndCleanPath(prefix)
	return prefix == "/" || path == prefix || strings.HasPrefix(path, prefix+"/")
}

func objParentPath(obj model.Obj) string {
	path := obj.GetPath()
	if path == "" {
		path = "/" + obj.GetName()
	}
	return stdpath.Dir(utils.FixAndCleanPath(path))
}

func splitPath(p string) []string {
	p = strings.Trim(utils.FixAndCleanPath(p), "/")
	if p == "" {
		return nil
	}
	return strings.Split(p, "/")
}

var _ driver.Driver = (*FastWebdavCrypt)(nil)
var _ driver.Getter = (*FastWebdavCrypt)(nil)
