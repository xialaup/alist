package halalcloud_open

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"path"
	"strings"
	"time"

	"github.com/alist-org/alist/v3/internal/driver"
	"github.com/alist-org/alist/v3/internal/model"
	"github.com/alist-org/alist/v3/internal/op"
	"github.com/alist-org/alist/v3/pkg/utils"
	"github.com/halalcloud/golang-sdk-lite/halalcloud/apiclient"
	sdkClient "github.com/halalcloud/golang-sdk-lite/halalcloud/apiclient"
	sdkModel "github.com/halalcloud/golang-sdk-lite/halalcloud/model"
	sdkOffline "github.com/halalcloud/golang-sdk-lite/halalcloud/services/offline"
	sdkUser "github.com/halalcloud/golang-sdk-lite/halalcloud/services/user"
	sdkUserFile "github.com/halalcloud/golang-sdk-lite/halalcloud/services/userfile"
	"github.com/ipfs/go-cid"
	log "github.com/sirupsen/logrus"
)

type HalalCloudOpen struct {
	*halalCommon
	model.Storage
	Addition
	sdkClient          *sdkClient.Client
	sdkOfflineService  *sdkOffline.OfflineTaskService
	sdkUserFileService *sdkUserFile.UserFileService
	sdkUserService     *sdkUser.UserService
	uploadThread       int
}

func (d *HalalCloudOpen) Config() driver.Config {
	return config
}

func (d *HalalCloudOpen) GetAddition() driver.Additional {
	return &d.Addition
}

func (d *HalalCloudOpen) Init(ctx context.Context) error {
	d.uploadThread = d.UploadThread
	if d.uploadThread < 1 || d.uploadThread > 32 {
		d.uploadThread, d.UploadThread = 3, 3
	}
	if d.halalCommon == nil {
		d.halalCommon = &halalCommon{
			UserInfo: &sdkUser.User{},
			refreshTokenFunc: func(token string) error {
				d.Addition.RefreshToken = token
				op.MustSaveDriverStorage(d)
				return nil
			},
		}
	}
	if d.Addition.RefreshToken != "" {
		d.halalCommon.SetRefreshToken(d.Addition.RefreshToken)
	}
	timeout := d.Addition.TimeOut
	if timeout <= 0 {
		timeout = 60
	}
	host := d.Addition.Host
	if host == "" {
		host = "openapi.2dland.cn"
	}

	client := apiclient.NewClient(nil, host, d.Addition.ClientID, d.Addition.ClientSecret, d.halalCommon, apiclient.WithTimeout(time.Second*time.Duration(timeout)))
	d.sdkClient = client
	d.sdkOfflineService = sdkOffline.NewOfflineTaskService(client)
	d.sdkUserFileService = sdkUserFile.NewUserFileService(client)
	d.sdkUserService = sdkUser.NewUserService(client)
	userInfo, err := d.sdkUserService.Get(ctx, &sdkUser.User{})
	if err != nil {
		return err
	}
	d.halalCommon.UserInfo = userInfo
	return nil
}

func (d *HalalCloudOpen) Drop(ctx context.Context) error {
	return nil
}

func (d *HalalCloudOpen) List(ctx context.Context, dir model.Obj, args model.ListArgs) ([]model.Obj, error) {
	return d.getFiles(ctx, dir)
}

func (d *HalalCloudOpen) Link(ctx context.Context, file model.Obj, args model.LinkArgs) (*model.Link, error) {
	return d.getLink(ctx, file)
}

func (d *HalalCloudOpen) MakeDir(ctx context.Context, parentDir model.Obj, dirName string) (model.Obj, error) {
	return d.makeDir(ctx, parentDir, dirName)
}

func (d *HalalCloudOpen) Move(ctx context.Context, srcObj, dstDir model.Obj) (model.Obj, error) {
	return d.move(ctx, srcObj, dstDir)
}

func (d *HalalCloudOpen) Rename(ctx context.Context, srcObj model.Obj, newName string) (model.Obj, error) {
	return d.rename(ctx, srcObj, newName)
}

func (d *HalalCloudOpen) Copy(ctx context.Context, srcObj, dstDir model.Obj) (model.Obj, error) {
	return d.copy(ctx, srcObj, dstDir)
}

func (d *HalalCloudOpen) Remove(ctx context.Context, obj model.Obj) error {
	return d.remove(ctx, obj)
}

func (d *HalalCloudOpen) Put(ctx context.Context, dstDir model.Obj, stream model.FileStreamer, up driver.UpdateProgress) (model.Obj, error) {
	return d.put(ctx, dstDir, stream, up)
}

func (d *HalalCloudOpen) Offline(ctx context.Context, args model.OtherArgs) (interface{}, error) {
	_, err := d.sdkOfflineService.Add(ctx, &sdkOffline.UserTask{
		Url:      fmt.Sprintf("%s", args.Data),
		SavePath: args.Obj.GetPath(),
	})
	if err != nil {
		return nil, err
	}
	return "ok", nil
}

func (d *HalalCloudOpen) Other(ctx context.Context, args model.OtherArgs) (interface{}, error) {
	dataBytes, err := json.Marshal(args.Data)
	if err != nil {
		return nil, fmt.Errorf("解析data数据出错: %w ,注意data为json格式", err)
	}
	if string(dataBytes) == "null" || string(dataBytes) == "{}" || string(dataBytes) == "\"\"" {
		return nil, fmt.Errorf("data不能为空")
	}

	jsonStr := string(dataBytes)
	jsonStr = strings.ReplaceAll(jsonStr, "__FILEID__", args.Obj.GetID())
	jsonStr = strings.ReplaceAll(jsonStr, "__FILENAME__", args.Obj.GetName())
	jsonStr = strings.ReplaceAll(jsonStr, "__FILEPATH__", args.Path)

	var req struct {
		Action string          `json:"action"`
		Method string          `json:"method"`
		Body   json.RawMessage `json:"body"`
	}
	if err := json.Unmarshal([]byte(jsonStr), &req); err != nil {
		return nil, fmt.Errorf("data信息传递错误: %w", err)
	}
	action := strings.ToUpper(req.Action)
	if action == "" {
		return nil, fmt.Errorf("请传递action字段")
	}
	method := strings.ToUpper(req.Method)
	body := req.Body
	if len(body) == 0 {
		body = []byte("{}")
	}

	if action != "OFFLINE" {
		return nil, fmt.Errorf("未知的action类型: %s", action)
	}
	if method == "" {
		method = "LIST"
	}
	switch method {
	case "LIST":
		var offlineRequestBody sdkOffline.OfflineTaskListRequest
		if err := json.Unmarshal(body, &offlineRequestBody); err != nil {
			return nil, fmt.Errorf("body信息传递错误: %s", err.Error())
		}
		return d.sdkOfflineService.List(ctx, &offlineRequestBody)
	case "PARSE":
		var parseOfflineRequestBody sdkOffline.TaskParseRequest
		if err := json.Unmarshal(body, &parseOfflineRequestBody); err != nil {
			return nil, fmt.Errorf("body信息传递错误: %s", err.Error())
		}
		return d.sdkOfflineService.Parse(ctx, &parseOfflineRequestBody)
	case "ADD":
		var addOfflineRequestBody sdkOffline.UserTask
		if err := json.Unmarshal(body, &addOfflineRequestBody); err != nil {
			return nil, fmt.Errorf("body信息传递错误: %s", err.Error())
		}
		return d.sdkOfflineService.Add(ctx, &addOfflineRequestBody)
	case "DELETE":
		var deleteOfflineRequestBody sdkOffline.OfflineTaskDeleteRequest
		if err := json.Unmarshal(body, &deleteOfflineRequestBody); err != nil {
			return nil, fmt.Errorf("body信息传递错误: %s", err.Error())
		}
		return d.sdkOfflineService.Delete(ctx, &deleteOfflineRequestBody)
	default:
		return nil, fmt.Errorf("未知的method类型: %s", method)
	}
}

var _ driver.Driver = (*HalalCloudOpen)(nil)

type ObjFile struct {
	sdkFile    *sdkUserFile.File
	fileSize   int64
	modTime    time.Time
	createTime time.Time
}

func NewObjFile(f *sdkUserFile.File) model.Obj {
	ofile := &ObjFile{sdkFile: f}
	ofile.fileSize = f.Size
	ofile.modTime = time.UnixMilli(f.UpdateTs)
	ofile.createTime = time.UnixMilli(f.CreateTs)
	return ofile
}

func (f *ObjFile) GetSize() int64 {
	return f.fileSize
}

func (f *ObjFile) GetName() string {
	return f.sdkFile.Name
}

func (f *ObjFile) ModTime() time.Time {
	return f.modTime
}

func (f *ObjFile) CreateTime() time.Time {
	return f.createTime
}

func (f *ObjFile) IsDir() bool {
	return f.sdkFile.Dir
}

func (f *ObjFile) GetHash() utils.HashInfo {
	return utils.HashInfo{}
}

func (f *ObjFile) GetID() string {
	return f.sdkFile.Identity
}

func (f *ObjFile) GetPath() string {
	return f.sdkFile.Path
}

func (d *HalalCloudOpen) getFiles(ctx context.Context, dir model.Obj) ([]model.Obj, error) {
	files := make([]model.Obj, 0)
	limit := int64(100)
	token := ""

	for {
		result, err := d.sdkUserFileService.List(ctx, &sdkUserFile.FileListRequest{
			Parent: &sdkUserFile.File{Path: dir.GetPath()},
			ListInfo: &sdkModel.ScanListRequest{
				Limit: limit,
				Token: token,
			},
		})
		if err != nil {
			return nil, err
		}
		for i := 0; i < len(result.Files); i++ {
			files = append(files, NewObjFile(result.Files[i]))
		}
		if result.ListInfo == nil || result.ListInfo.Token == "" {
			break
		}
		token = result.ListInfo.Token
	}
	return files, nil
}

func (d *HalalCloudOpen) getLink(ctx context.Context, file model.Obj) (*model.Link, error) {
	fid := file.GetID()
	fpath := file.GetPath()
	if fid != "" {
		fpath = ""
	}
	fi, err := d.sdkUserFileService.GetDirectDownloadAddress(ctx, &sdkUserFile.DirectDownloadRequest{
		Identity: fid,
		Path:     fpath,
	})
	if err != nil {
		return nil, err
	}
	duration := time.Until(time.UnixMilli(fi.ExpireAt))
	return &model.Link{
		URL:        fi.DownloadAddress,
		Expiration: &duration,
	}, nil
}

func (d *HalalCloudOpen) makeDir(ctx context.Context, dir model.Obj, name string) (model.Obj, error) {
	_, err := d.sdkUserFileService.Create(ctx, &sdkUserFile.File{
		Path: dir.GetPath(),
		Name: name,
	})
	return nil, err
}

func (d *HalalCloudOpen) move(ctx context.Context, obj model.Obj, dir model.Obj) (model.Obj, error) {
	_, err := d.sdkUserFileService.Move(ctx, &sdkUserFile.BatchOperationRequest{
		Source: []*sdkUserFile.File{{Path: obj.GetPath()}},
		Dest:   &sdkUserFile.File{Path: dir.GetPath()},
	})
	return nil, err
}

func (d *HalalCloudOpen) rename(ctx context.Context, obj model.Obj, name string) (model.Obj, error) {
	_, err := d.sdkUserFileService.Rename(ctx, &sdkUserFile.File{
		Path: obj.GetPath(),
		Name: name,
	})
	return nil, err
}

func (d *HalalCloudOpen) copy(ctx context.Context, obj model.Obj, dir model.Obj) (model.Obj, error) {
	id := obj.GetID()
	sourcePath := obj.GetPath()
	if id != "" {
		sourcePath = ""
	}
	destID := dir.GetID()
	destPath := dir.GetPath()
	if destID != "" {
		destPath = ""
	}
	_, err := d.sdkUserFileService.Copy(ctx, &sdkUserFile.BatchOperationRequest{
		Source: []*sdkUserFile.File{{Path: sourcePath, Identity: id}},
		Dest:   &sdkUserFile.File{Path: destPath, Identity: destID},
	})
	return nil, err
}

func (d *HalalCloudOpen) remove(ctx context.Context, obj model.Obj) error {
	_, err := d.sdkUserFileService.Delete(ctx, &sdkUserFile.BatchOperationRequest{
		Source: []*sdkUserFile.File{{Identity: obj.GetID(), Path: obj.GetPath()}},
	})
	return err
}

func (d *HalalCloudOpen) put(ctx context.Context, dstDir model.Obj, fileStream model.FileStreamer, up driver.UpdateProgress) (model.Obj, error) {
	newPath := path.Join(dstDir.GetPath(), fileStream.GetName())
	uploadTask, err := d.sdkUserFileService.CreateUploadTask(ctx, &sdkUserFile.File{
		Path: newPath,
		Size: fileStream.GetSize(),
	})
	if err != nil {
		return nil, err
	}
	if uploadTask.Created {
		return nil, nil
	}

	codec := uint64(0x55)
	if uploadTask.BlockCodec > 0 {
		codec = uint64(uploadTask.BlockCodec)
	}
	mhType := uint64(0x12)
	if uploadTask.BlockHashType > 0 {
		mhType = uint64(uploadTask.BlockHashType)
	}
	prefix := cid.Prefix{
		Codec:    codec,
		MhLength: -1,
		MhType:   mhType,
		Version:  1,
	}

	slicesList := make([]string, 0)
	buffer := make([]byte, int(uploadTask.BlockSize))
	offset := 0
	teeReader := io.TeeReader(fileStream, driver.NewProgress(fileStream.GetSize(), up))
	for {
		n, err := teeReader.Read(buffer[offset:])
		if n > 0 {
			offset += n
			if offset == len(buffer) {
				uploadCid, err := postFileSlice(ctx, buffer, uploadTask.Task, uploadTask.UploadAddress, prefix, retryTimes)
				if err != nil {
					return nil, err
				}
				slicesList = append(slicesList, uploadCid.String())
				offset = 0
			}
		}
		if err != nil {
			if err == io.EOF {
				if offset > 0 {
					uploadCid, err := postFileSlice(ctx, buffer[:offset], uploadTask.Task, uploadTask.UploadAddress, prefix, retryTimes)
					if err != nil {
						return nil, err
					}
					slicesList = append(slicesList, uploadCid.String())
				}
				break
			}
			return nil, err
		}
	}

	newFile, err := makeFile(ctx, slicesList, uploadTask.Task, uploadTask.UploadAddress, retryTimes)
	if err != nil {
		return nil, err
	}
	return NewObjFile(newFile), nil
}

func makeFile(ctx context.Context, fileSlice []string, taskID string, uploadAddress string, retry int) (*sdkUserFile.File, error) {
	var lastError error
	for i := 0; i < retry; i++ {
		newFile, err := doMakeFile(fileSlice, taskID, uploadAddress)
		if err == nil {
			return newFile, nil
		}
		if ctx.Err() != nil || strings.Contains(err.Error(), "not found") {
			return nil, err
		}
		lastError = err
		log.Errorf("make file slice failed, retrying... error: %s", err.Error())
		time.Sleep(slicePostErrorRetryInterval)
	}
	return nil, fmt.Errorf("mk file slice failed after %d times, error: %w", retry, lastError)
}

func doMakeFile(fileSlice []string, taskID string, uploadAddress string) (*sdkUserFile.File, error) {
	accessURL := uploadAddress + "/" + taskID
	u, err := url.Parse(accessURL)
	if err != nil {
		return nil, err
	}
	body, _ := json.Marshal(fileSlice)
	httpRequest := http.Request{
		Method: http.MethodPost,
		URL:    u,
		Header: map[string][]string{
			"Accept":       {"application/json"},
			"Content-Type": {"application/json"},
		},
		Body: io.NopCloser(bytes.NewReader(body)),
	}
	httpClient := http.Client{Timeout: time.Minute * 2}
	httpResponse, err := httpClient.Do(&httpRequest)
	if err != nil {
		return nil, err
	}
	defer httpResponse.Body.Close()
	if httpResponse.StatusCode != http.StatusOK && httpResponse.StatusCode != http.StatusCreated {
		b, _ := io.ReadAll(httpResponse.Body)
		return nil, fmt.Errorf("mk file slice failed, status code: %d, message: %s", httpResponse.StatusCode, string(b))
	}
	b, _ := io.ReadAll(httpResponse.Body)
	var result UploadedFile
	if err := json.Unmarshal(b, &result); err != nil {
		return nil, err
	}
	return &sdkUserFile.File{
		Identity:        result.Identity,
		Path:            result.Path,
		Size:            result.Size,
		ContentIdentity: result.ContentIdentity,
	}, nil
}

func postFileSlice(ctx context.Context, fileSlice []byte, taskID string, uploadAddress string, prefix cid.Prefix, retry int) (cid.Cid, error) {
	var lastError error
	for i := 0; i < retry; i++ {
		newCid, err := doPostFileSlice(fileSlice, taskID, uploadAddress, prefix)
		if err == nil {
			return newCid, nil
		}
		if ctx.Err() != nil {
			return cid.Undef, err
		}
		lastError = err
		time.Sleep(slicePostErrorRetryInterval)
	}
	return cid.Undef, fmt.Errorf("upload file slice failed after %d times, error: %w", retry, lastError)
}

func doPostFileSlice(fileSlice []byte, taskID string, uploadAddress string, prefix cid.Prefix) (cid.Cid, error) {
	newCid, err := prefix.Sum(fileSlice)
	if err != nil {
		return cid.Undef, err
	}
	accessURL := uploadAddress + "/" + taskID + "/" + newCid.String()
	u, err := url.Parse(accessURL)
	if err != nil {
		return cid.Undef, err
	}
	httpClient := http.Client{Timeout: time.Second * 30}
	httpRequest := http.Request{
		Method: http.MethodGet,
		URL:    u,
		Header: map[string][]string{"Accept": {"application/json"}},
	}
	httpResponse, err := httpClient.Do(&httpRequest)
	if err != nil {
		log.Errorf("access %s failed, method: %s", accessURL, http.MethodGet)
		return cid.Undef, err
	}
	b, readErr := io.ReadAll(httpResponse.Body)
	httpResponse.Body.Close()
	if readErr != nil {
		return cid.Undef, readErr
	}
	if httpResponse.StatusCode != http.StatusOK {
		return cid.Undef, fmt.Errorf("upload file slice failed, status code: %d", httpResponse.StatusCode)
	}
	var exists bool
	if err := json.Unmarshal(b, &exists); err != nil {
		return cid.Undef, err
	}
	if exists {
		return newCid, nil
	}

	httpRequest = http.Request{
		Method: http.MethodPost,
		URL:    u,
		Header: map[string][]string{
			"Accept":       {"application/json"},
			"Content-Type": {"application/octet-stream"},
		},
		Body: io.NopCloser(bytes.NewReader(fileSlice)),
	}
	httpResponse, err = httpClient.Do(&httpRequest)
	if err != nil {
		return cid.Undef, err
	}
	defer httpResponse.Body.Close()
	if httpResponse.StatusCode != http.StatusOK && httpResponse.StatusCode != http.StatusCreated {
		b, _ := io.ReadAll(httpResponse.Body)
		return cid.Undef, fmt.Errorf("upload file slice failed, status code: %d, message: %s", httpResponse.StatusCode, string(b))
	}
	return newCid, nil
}
