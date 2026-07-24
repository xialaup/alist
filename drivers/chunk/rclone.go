package chunk

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	stdpath "path"
	"regexp"
	"strconv"
	"strings"

	"github.com/alist-org/alist/v3/internal/driver"
	"github.com/alist-org/alist/v3/internal/model"
	"github.com/alist-org/alist/v3/internal/op"
	"github.com/alist-org/alist/v3/internal/stream"
	"github.com/alist-org/alist/v3/pkg/http_range"
	"github.com/alist-org/alist/v3/pkg/utils"
)

const rcloneMaxMetadataSize = 255

const (
	rcloneDefaultNameFormat = "*.rclone_chunk.###"
	rcloneDefaultStartFrom  = 1
)

type rcloneNameFormat struct {
	dataNameFmt string
	nameRegexp  *regexp.Regexp
	startFrom   int
}

type rcloneMetadata struct {
	Version  *int   `json:"ver"`
	Size     *int64 `json:"size"`
	ChunkNum *int   `json:"nchunks"`
	MD5      string `json:"md5,omitempty"`
	SHA1     string `json:"sha1,omitempty"`
	XactID   string `json:"txn,omitempty"`
}

func newRcloneNameFormat(pattern, startFromText string) (*rcloneNameFormat, error) {
	if pattern == "" {
		return nil, nil
	}
	if strings.Count(pattern, "*") != 1 {
		return nil, fmt.Errorf("rclone name_format must have exactly one asterisk (*)")
	}
	numDigits := strings.Count(pattern, "#")
	if numDigits < 1 {
		return nil, fmt.Errorf("rclone name_format must have a hash character (#)")
	}
	if strings.Index(pattern, "*") > strings.Index(pattern, "#") {
		return nil, fmt.Errorf("asterisk (*) in rclone name_format must come before hashes (#)")
	}
	if ok, _ := regexp.MatchString("^[^#]*[#]+[^#]*$", pattern); !ok {
		return nil, fmt.Errorf("hashes (#) in rclone name_format must be consecutive")
	}
	if dir, _ := stdpath.Split(pattern); dir != "" {
		return nil, fmt.Errorf("directory separator prohibited in rclone name_format")
	}
	if pattern[0] != '*' {
		return nil, fmt.Errorf("rclone name_format must start with asterisk (*)")
	}

	startFrom := rcloneDefaultStartFrom
	if startFromText != "" {
		parsed, err := strconv.Atoi(startFromText)
		if err != nil || parsed < 0 {
			return nil, fmt.Errorf("rclone_start_from must be a non-negative integer")
		}
		startFrom = parsed
	}

	reHashes := regexp.MustCompile("[#]+")
	reDigits := "[0-9]+"
	if numDigits > 1 {
		reDigits = fmt.Sprintf("[0-9]{%d,}", numDigits)
	}
	strRegex := regexp.QuoteMeta(pattern)
	strRegex = reHashes.ReplaceAllLiteralString(strRegex, "("+reDigits+")")
	strRegex = strings.ReplaceAll(strRegex, "\\*", "(.+?)")
	strRegex = fmt.Sprintf("^%s(?:_([0-9a-z]{4,9})|\\.\\.tmp_[0-9]{10,13})?$", strRegex)

	fmtDigits := "%d"
	if numDigits > 1 {
		fmtDigits = fmt.Sprintf("%%0%dd", numDigits)
	}
	strFmt := strings.ReplaceAll(pattern, "%", "%%")
	strFmt = strings.Replace(strFmt, "*", "%s", 1)
	strFmt = reHashes.ReplaceAllLiteralString(strFmt, fmtDigits)

	return &rcloneNameFormat{
		dataNameFmt: strFmt,
		nameRegexp:  regexp.MustCompile(strRegex),
		startFrom:   startFrom,
	}, nil
}

func (f *rcloneNameFormat) parseChunkName(name string) (base string, idx int, xactID string, ok bool) {
	if f == nil {
		return "", -1, "", false
	}
	match := f.nameRegexp.FindStringSubmatch(name)
	if match == nil {
		return "", -1, "", false
	}
	chunkNo, err := strconv.Atoi(match[2])
	if err != nil {
		return "", -1, "", false
	}
	chunkNo -= f.startFrom
	if chunkNo < 0 {
		return "", -1, "", false
	}
	xactID = match[3]
	return match[1], chunkNo, xactID, true
}

func (f *rcloneNameFormat) partName(name string, idx int, xactID string) string {
	part := fmt.Sprintf(f.dataNameFmt, name, idx+f.startFrom)
	if xactID != "" {
		part += "_" + xactID
	}
	return part
}

func readRcloneMetadata(ctx context.Context, storage driver.Driver, path string, obj model.Obj) (*rcloneMetadata, error) {
	if obj == nil || obj.IsDir() || obj.GetSize() > rcloneMaxMetadataSize {
		return nil, fmt.Errorf("not rclone metadata")
	}
	link, _, err := op.Link(ctx, storage, path, model.LinkArgs{})
	if err != nil {
		return nil, err
	}
	reader, closer, err := linkReader(ctx, link, obj.GetSize())
	if err != nil {
		return nil, err
	}
	if closer != nil {
		defer closer.Close()
	}
	data, err := io.ReadAll(io.LimitReader(reader, rcloneMaxMetadataSize+1))
	if err != nil {
		return nil, err
	}
	if len(data) < 2 || len(data) > rcloneMaxMetadataSize || data[0] != '{' || data[len(data)-1] != '}' {
		return nil, fmt.Errorf("not rclone metadata")
	}
	var meta rcloneMetadata
	if err := json.Unmarshal(data, &meta); err != nil {
		return nil, err
	}
	if meta.Version == nil || meta.Size == nil || meta.ChunkNum == nil || *meta.Version < 1 || *meta.Version > 2 || *meta.Size < 0 || *meta.ChunkNum < 1 {
		return nil, fmt.Errorf("invalid rclone metadata")
	}
	return &meta, nil
}

func linkReader(ctx context.Context, link *model.Link, size int64) (io.Reader, io.Closer, error) {
	if link.RangeReadCloser != nil {
		r, err := link.RangeReadCloser.RangeRead(ctx, http_range.Range{Start: 0, Length: size})
		return r, r, err
	}
	if link.MFile != nil {
		_, err := link.MFile.Seek(0, io.SeekStart)
		return link.MFile, link.MFile, err
	}
	if link.URL != "" {
		rrc, err := stream.GetRangeReadCloserFromLink(size, link)
		if err != nil {
			return nil, nil, err
		}
		r, err := rrc.RangeRead(ctx, http_range.Range{Start: 0, Length: size})
		return r, r, err
	}
	return nil, nil, fmt.Errorf("link has no readable body")
}

func rcloneHashInfo(meta *rcloneMetadata) utils.HashInfo {
	h := make(map[*utils.HashType]string)
	if meta.MD5 != "" {
		if ht, ok := utils.GetHashByName("md5"); ok {
			h[ht] = meta.MD5
		}
	}
	if meta.SHA1 != "" {
		if ht, ok := utils.GetHashByName("sha1"); ok {
			h[ht] = meta.SHA1
		}
	}
	return utils.NewHashInfoByMap(h)
}

func makeRcloneObject(path string, metaObj model.Obj, meta *rcloneMetadata, chunkSizes []int64) *chunkObject {
	dir, name := stdpath.Split(path)
	return &chunkObject{
		Object: model.Object{
			Path:     stdpath.Join(dir, name),
			Name:     name,
			Size:     *meta.Size,
			Modified: metaObj.ModTime(),
			Ctime:    metaObj.CreateTime(),
			HashInfo: rcloneHashInfo(meta),
		},
		chunkSizes:   chunkSizes,
		partDir:      dir,
		rclone:       true,
		rcloneXactID: meta.XactID,
	}
}

func buildRcloneObject(ctx context.Context, storage driver.Driver, format *rcloneNameFormat, metaPath, objPath string, metaObj model.Obj, entries []model.Obj) (*chunkObject, error) {
	if format == nil {
		return nil, fmt.Errorf("rclone compatibility disabled")
	}
	meta, err := readRcloneMetadata(ctx, storage, metaPath, metaObj)
	if err != nil {
		return nil, err
	}
	_, name := stdpath.Split(metaPath)
	chunkSizes := make([]int64, *meta.ChunkNum)
	var total int64
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		base, idx, xactID, ok := format.parseChunkName(entry.GetName())
		if !ok || base != name || idx < 0 || idx >= *meta.ChunkNum || xactID != meta.XactID {
			continue
		}
		chunkSizes[idx] = entry.GetSize()
		total += entry.GetSize()
	}
	for idx, size := range chunkSizes {
		if size == 0 && (*meta.Size > 0 || idx != len(chunkSizes)-1) {
			return nil, fmt.Errorf("rclone chunk part[%d] are missing", idx)
		}
	}
	if total != *meta.Size {
		return nil, fmt.Errorf("rclone metadata doesn't match file size")
	}
	return makeRcloneObject(objPath, metaObj, meta, chunkSizes), nil
}

func (d *Chunk) putRclone(ctx context.Context, storage driver.Driver, rootPath string, dstDir model.Obj, file model.FileStreamer, upReader io.Reader) error {
	if d.rcloneFormat == nil {
		return fmt.Errorf("rclone name format is not configured")
	}
	dst := stdpath.Join(rootPath, dstDir.GetPath())
	d.cleanupRcloneUpload(ctx, storage, dst, file.GetName())
	uploaded := make([]string, 0)
	cleanup := func() {
		for _, path := range uploaded {
			_ = op.Remove(ctx, storage, path)
		}
		_ = op.Remove(ctx, storage, stdpath.Join(dst, file.GetName()))
	}

	fullPartCount := int(file.GetSize() / d.PartSize)
	tailSize := file.GetSize() % d.PartSize
	if tailSize == 0 && fullPartCount > 0 {
		fullPartCount--
		tailSize = d.PartSize
	}
	partIndex := 0
	for partIndex < fullPartCount {
		partPath := stdpath.Join(dst, d.rcloneFormat.partName(file.GetName(), partIndex, ""))
		err := op.Put(ctx, storage, dst, &stream.FileStream{
			Obj: &model.Object{
				Name:     d.rcloneFormat.partName(file.GetName(), partIndex, ""),
				Size:     d.PartSize,
				Modified: file.ModTime(),
			},
			Mimetype: file.GetMimetype(),
			Reader:   io.LimitReader(upReader, d.PartSize),
		}, nil, true)
		if err != nil {
			cleanup()
			return err
		}
		uploaded = append(uploaded, partPath)
		partIndex++
	}

	lastPartName := d.rcloneFormat.partName(file.GetName(), fullPartCount, "")
	lastPartPath := stdpath.Join(dst, lastPartName)
	err := op.Put(ctx, storage, dst, &stream.FileStream{
		Obj: &model.Object{
			Name:     lastPartName,
			Size:     tailSize,
			Modified: file.ModTime(),
		},
		Mimetype: file.GetMimetype(),
		Reader:   upReader,
	}, nil, true)
	if err != nil {
		cleanup()
		return err
	}
	uploaded = append(uploaded, lastPartPath)

	metadata, err := makeRcloneMetadata(file, fullPartCount+1)
	if err != nil {
		cleanup()
		return err
	}
	err = op.Put(ctx, storage, dst, &stream.FileStream{
		Obj: &model.Object{
			Name:     file.GetName(),
			Size:     int64(len(metadata)),
			Modified: file.ModTime(),
		},
		Mimetype: "application/json",
		Reader:   bytes.NewReader(metadata),
	}, nil)
	if err != nil {
		cleanup()
	}
	return err
}

func (d *Chunk) cleanupRcloneUpload(ctx context.Context, storage driver.Driver, dst, name string) {
	_ = op.Remove(ctx, storage, stdpath.Join(dst, name))
	_ = op.Remove(ctx, storage, stdpath.Join(dst, "[openlist_chunk]"+name))
	entries, err := op.List(ctx, storage, dst, model.ListArgs{})
	if err != nil {
		return
	}
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		base, _, _, ok := d.rcloneFormat.parseChunkName(entry.GetName())
		if ok && base == name {
			_ = op.Remove(ctx, storage, stdpath.Join(dst, entry.GetName()))
		}
	}
}

func makeRcloneMetadata(file model.FileStreamer, nChunks int) ([]byte, error) {
	version := 1
	metadata := rcloneMetadata{
		Version:  &version,
		Size:     ptr(file.GetSize()),
		ChunkNum: &nChunks,
		MD5:      file.GetHash().GetHash(utils.MD5),
		SHA1:     file.GetHash().GetHash(utils.SHA1),
	}
	return json.Marshal(&metadata)
}

func ptr[T any](v T) *T {
	return &v
}
