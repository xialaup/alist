package halalcloud

import (
	"context"
	"crypto/sha1"
	"encoding/json"
	"fmt"
	"io"
	"net/url"
	"path"
	"strconv"
	"strings"
	"time"

	"github.com/alist-org/alist/v3/drivers/base"
	"github.com/alist-org/alist/v3/internal/driver"
	"github.com/alist-org/alist/v3/internal/model"
	"github.com/alist-org/alist/v3/internal/op"
	"github.com/alist-org/alist/v3/pkg/http_range"
	"github.com/alist-org/alist/v3/pkg/utils"
	"github.com/aws/aws-sdk-go/aws"
	"github.com/aws/aws-sdk-go/aws/credentials"
	"github.com/aws/aws-sdk-go/aws/session"
	"github.com/aws/aws-sdk-go/service/s3/s3manager"
	"github.com/city404/v6-public-rpc-proto/go/v6/common"
	pubFileShare "github.com/city404/v6-public-rpc-proto/go/v6/fileshare"
	pubUserOffline "github.com/city404/v6-public-rpc-proto/go/v6/offline"
	pbPublicUser "github.com/city404/v6-public-rpc-proto/go/v6/user"
	pubUserFile "github.com/city404/v6-public-rpc-proto/go/v6/userfile"
	pbDavConfig "github.com/city404/v6-public-rpc-proto/go/v6/webdavconfig"
	"github.com/pkg/errors"
	"github.com/rclone/rclone/lib/readers"
	"github.com/tidwall/gjson"
	"github.com/zzzhr1990/go-common-entity/userfile"
)

type HalalCloud struct {
	*HalalCommon
	model.Storage
	Addition

	uploadThread int
}

func (d *HalalCloud) Config() driver.Config {
	return config
}

func (d *HalalCloud) GetAddition() driver.Additional {
	return &d.Addition
}

func (d *HalalCloud) Init(ctx context.Context) error {
	d.uploadThread, _ = strconv.Atoi(d.UploadThread)
	if d.uploadThread < 1 || d.uploadThread > 32 {
		d.uploadThread, d.UploadThread = 3, "3"
	}

	if d.HalalCommon == nil {
		d.HalalCommon = &HalalCommon{
			Common: &Common{},
			AuthService: &AuthService{
				appID: func() string {
					if d.Addition.AppID != "" {
						return d.Addition.AppID
					}
					return AppID
				}(),
				appVersion: func() string {
					if d.Addition.AppVersion != "" {
						return d.Addition.AppVersion
					}
					return AppVersion
				}(),
				appSecret: func() string {
					if d.Addition.AppSecret != "" {
						return d.Addition.AppSecret
					}
					return AppSecret
				}(),
				tr: &TokenResp{
					RefreshToken: d.Addition.RefreshToken,
				},
			},
			UserInfo: &UserInfo{},
			refreshTokenFunc: func(token string) error {
				d.Addition.RefreshToken = token
				op.MustSaveDriverStorage(d)
				return nil
			},
		}
	}

	// 防止重复登录
	if d.Addition.RefreshToken == "" || !d.IsLogin() {
		as, err := d.NewAuthServiceWithOauth()
		if err != nil {
			d.GetStorage().SetStatus(fmt.Sprintf("%+v", err.Error()))
			return err
		}
		d.HalalCommon.AuthService = as
		d.SetTokenResp(as.tr)
		result, err := pbDavConfig.NewPubDavConfigClient(d.HalalCommon.serv.GetGrpcConnection()).Get(ctx, &pbDavConfig.DavConfig{})
		if err != nil {
			fmt.Println(fmt.Errorf("无法获取Webdav信息: %w", err))
			return err
		}
		if len(result.Username) > 0 {
			d.SetWebDavUserName(result.Username)
			d.SetWebDavPassWord(result.Password)
		} else {
			fmt.Println(fmt.Errorf("无法获取Webdav用户信息"))
		}
		op.MustSaveDriverStorage(d)
	}
	var err error
	d.HalalCommon.serv, err = d.NewAuthService(d.Addition.RefreshToken)

	if err != nil {
		return err
	}

	result, err := pbDavConfig.NewPubDavConfigClient(d.HalalCommon.serv.GetGrpcConnection()).Get(ctx, &pbDavConfig.DavConfig{})
	if err != nil {
		fmt.Println(fmt.Errorf("无法获取Webdav信息: %w", err))
		return err
	}
	if len(result.Username) > 0 {
		d.SetWebDavUserName(result.Username)
		d.SetWebDavPassWord(result.Password)
	} else {
		fmt.Println(fmt.Errorf("无法获取Webdav用户信息"))
	}

	return nil
}

func (d *HalalCloud) Drop(ctx context.Context) error {
	return nil
}

func (d *HalalCloud) List(ctx context.Context, dir model.Obj, args model.ListArgs) ([]model.Obj, error) {
	return d.getFiles(ctx, dir)
}

func (d *HalalCloud) Link(ctx context.Context, file model.Obj, args model.LinkArgs) (*model.Link, error) {
	return d.getLink(ctx, file, args)
}

func (d *HalalCloud) MakeDir(ctx context.Context, parentDir model.Obj, dirName string) (model.Obj, error) {
	return d.makeDir(ctx, parentDir, dirName)
}

func (d *HalalCloud) Move(ctx context.Context, srcObj, dstDir model.Obj) (model.Obj, error) {
	return d.move(ctx, srcObj, dstDir)
}

func (d *HalalCloud) Rename(ctx context.Context, srcObj model.Obj, newName string) (model.Obj, error) {
	return d.rename(ctx, srcObj, newName)
}

func (d *HalalCloud) Copy(ctx context.Context, srcObj, dstDir model.Obj) (model.Obj, error) {
	return d.copy(ctx, srcObj, dstDir)
}

func (d *HalalCloud) Remove(ctx context.Context, obj model.Obj) error {
	return d.remove(ctx, obj)
}

func (d *HalalCloud) Put(ctx context.Context, dstDir model.Obj, stream model.FileStreamer, up driver.UpdateProgress) (model.Obj, error) {
	return d.put(ctx, dstDir, stream, up)
}

func (d *HalalCloud) IsLogin() bool {
	if d.AuthService.tr == nil {
		return false
	}
	serv, err := d.NewAuthService(d.Addition.RefreshToken)
	if err != nil {
		return false
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	result, err := pbPublicUser.NewPubUserClient(serv.GetGrpcConnection()).Get(ctx, &pbPublicUser.User{
		Identity: "",
	})
	if result == nil || err != nil {
		return false
	}
	d.UserInfo.Identity = result.Identity
	d.UserInfo.CreateTs = result.CreateTs
	d.UserInfo.Name = result.Name
	d.UserInfo.UpdateTs = result.UpdateTs
	return true
}

type HalalCommon struct {
	*Common
	*AuthService     // 登录信息
	*UserInfo        // 用户信息
	refreshTokenFunc func(token string) error
	serv             *AuthService
}

func (d *HalalCloud) SetTokenResp(tr *TokenResp) {
	d.Addition.RefreshToken = tr.RefreshToken
}

func (d *HalalCloud) SetWebDavUserName(username string) {
	d.Addition.WebDavUserName = username
}

func (d *HalalCloud) SetWebDavPassWord(password string) {
	d.Addition.WebDavPassWord = password
}

func (d *HalalCloud) getFiles(ctx context.Context, dir model.Obj) ([]model.Obj, error) {

	files := make([]model.Obj, 0)
	limit := int64(100)
	token := ""
	client := pubUserFile.NewPubUserFileClient(d.HalalCommon.serv.GetGrpcConnection())

	opDir := d.GetCurrentDir(dir)

	for {
		result, err := client.List(ctx, &pubUserFile.FileListRequest{
			Parent: &pubUserFile.File{Path: opDir},
			ListInfo: &common.ScanListRequest{
				Limit: limit,
				Token: token,
			},
		})
		if err != nil {
			return nil, err
		}

		for i := 0; len(result.Files) > i; i++ {
			files = append(files, (*Files)(result.Files[i]))
		}

		if result.ListInfo == nil || result.ListInfo.Token == "" {
			break
		}
		token = result.ListInfo.Token

	}
	return files, nil
}

func (d *HalalCloud) getLink(ctx context.Context, file model.Obj, args model.LinkArgs) (*model.Link, error) {

	if d.UseDavMode {
		// 构造基础URL
		baseURL := "https://dav.2dland.cn"
		// 解析基础URL
		parsedURL, err := url.Parse(baseURL)
		if err != nil {
			return nil, err
		}

		// 设置用户名和密码
		username := d.WebDavUserName
		password := d.WebDavPassWord

		// 设置用户名和密码
		parsedURL.User = url.UserPassword(username, password)
		linkurl := parsedURL.String() + file.GetPath()

		link := &model.Link{
			URL: linkurl,
		}
		return link, nil
	}

	client := pubUserFile.NewPubUserFileClient(d.HalalCommon.serv.GetGrpcConnection())
	ctx1, cancelFunc := context.WithCancel(context.Background())
	defer cancelFunc()

	result, err := client.ParseFileSlice(ctx1, (*pubUserFile.File)(file.(*Files)))
	if err != nil {
		return nil, err
	}
	fileAddrs := []*pubUserFile.SliceDownloadInfo{}
	var addressDuration int64

	nodesNumber := len(result.RawNodes)
	nodesIndex := nodesNumber - 1
	startIndex, endIndex := 0, nodesIndex
	for nodesIndex >= 0 {
		if nodesIndex >= 200 {
			endIndex = 200
		} else {
			endIndex = nodesNumber
		}
		for ; endIndex <= nodesNumber; endIndex += 200 {
			if endIndex == 0 {
				endIndex = 1
			}
			sliceAddress, err := client.GetSliceDownloadAddress(ctx, &pubUserFile.SliceDownloadAddressRequest{
				Identity: result.RawNodes[startIndex:endIndex],
				Version:  1,
			})
			if err != nil {
				return nil, err
			}
			addressDuration = sliceAddress.ExpireAt
			fileAddrs = append(fileAddrs, sliceAddress.Addresses...)
			startIndex = endIndex
			nodesIndex -= 200
		}

	}

	size := result.FileSize
	chunks := getChunkSizes(result.Sizes)
	var finalClosers utils.Closers
	resultRangeReader := func(ctx context.Context, httpRange http_range.Range) (io.ReadCloser, error) {
		length := httpRange.Length
		if httpRange.Length >= 0 && httpRange.Start+httpRange.Length >= size {
			length = -1
		}
		if err != nil {
			return nil, fmt.Errorf("open download file failed: %w", err)
		}
		oo := &openObject{
			ctx:     ctx,
			d:       fileAddrs,
			chunk:   &[]byte{},
			chunks:  &chunks,
			skip:    httpRange.Start,
			sha:     result.Sha1,
			shaTemp: sha1.New(),
		}
		finalClosers.Add(oo)

		return readers.NewLimitedReadCloser(oo, length), nil
	}

	var duration time.Duration
	if addressDuration != 0 {
		duration = time.Until(time.UnixMilli(addressDuration))
	} else {
		duration = time.Until(time.Now().Add(time.Hour))
	}

	resultRangeReadCloser := &model.RangeReadCloser{RangeReader: resultRangeReader, Closers: finalClosers}
	return &model.Link{
		RangeReadCloser: resultRangeReadCloser,
		Expiration:      &duration,
	}, nil
}

func (d *HalalCloud) makeDir(ctx context.Context, dir model.Obj, name string) (model.Obj, error) {
	newDir := userfile.NewFormattedPath(d.GetCurrentOpDir(dir, []string{name}, 0)).GetPath()
	_, err := pubUserFile.NewPubUserFileClient(d.HalalCommon.serv.GetGrpcConnection()).Create(ctx, &pubUserFile.File{
		Path: newDir,
	})
	return nil, err
}

func (d *HalalCloud) move(ctx context.Context, obj model.Obj, dir model.Obj) (model.Obj, error) {
	oldDir := userfile.NewFormattedPath(d.GetCurrentDir(obj)).GetPath()
	newDir := userfile.NewFormattedPath(d.GetCurrentDir(dir)).GetPath()
	_, err := pubUserFile.NewPubUserFileClient(d.HalalCommon.serv.GetGrpcConnection()).Move(ctx, &pubUserFile.BatchOperationRequest{
		Source: []*pubUserFile.File{
			{
				Identity: obj.GetID(),
				Path:     oldDir,
			},
		},
		Dest: &pubUserFile.File{
			Identity: dir.GetID(),
			Path:     newDir,
		},
	})
	return nil, err
}

func (d *HalalCloud) rename(ctx context.Context, obj model.Obj, name string) (model.Obj, error) {
	id := obj.GetID()
	newPath := userfile.NewFormattedPath(d.GetCurrentOpDir(obj, []string{name}, 0)).GetPath()

	_, err := pubUserFile.NewPubUserFileClient(d.HalalCommon.serv.GetGrpcConnection()).Rename(ctx, &pubUserFile.File{
		Path:     newPath,
		Identity: id,
		Name:     name,
	})
	return nil, err
}

func (d *HalalCloud) copy(ctx context.Context, obj model.Obj, dir model.Obj) (model.Obj, error) {
	id := obj.GetID()
	sourcePath := userfile.NewFormattedPath(d.GetCurrentDir(obj)).GetPath()
	if len(id) > 0 {
		sourcePath = ""
	}
	dest := &pubUserFile.File{
		Identity: dir.GetID(),
		Path:     userfile.NewFormattedPath(d.GetCurrentDir(dir)).GetPath(),
	}
	_, err := pubUserFile.NewPubUserFileClient(d.HalalCommon.serv.GetGrpcConnection()).Copy(ctx, &pubUserFile.BatchOperationRequest{
		Source: []*pubUserFile.File{
			{
				Path:     sourcePath,
				Identity: id,
			},
		},
		Dest: dest,
	})
	return nil, err
}

func (d *HalalCloud) remove(ctx context.Context, obj model.Obj) error {
	id := obj.GetID()
	newPath := userfile.NewFormattedPath(d.GetCurrentDir(obj)).GetPath()
	//if len(id) > 0 {
	//	newPath = ""
	//}
	_, err := pubUserFile.NewPubUserFileClient(d.HalalCommon.serv.GetGrpcConnection()).Delete(ctx, &pubUserFile.BatchOperationRequest{
		Source: []*pubUserFile.File{
			{
				Path:     newPath,
				Identity: id,
			},
		},
	})
	return err
}

func (d *HalalCloud) Offline(ctx context.Context, args model.OtherArgs) (interface{}, error) {
	_, err := pubUserOffline.NewPubOfflineTaskClient(d.HalalCommon.serv.GetGrpcConnection()).Add(ctx, &pubUserOffline.UserTask{
		Url:      fmt.Sprintf("%s", args.Data),
		SavePath: args.Obj.GetPath(),
	})
	if err != nil {
		return nil, err
	}
	return "ok", nil
}

func (d *HalalCloud) Other(ctx context.Context, args model.OtherArgs) (interface{}, error) {

	if args.Data == nil {
		if args.Obj.IsDir() {
			return "无法获取文件夹的直链", errors.New("无法获取文件夹的直链")
		}

		if len(d.WebDavUserName) <= 0 {
			return "无法获取WebDav用户信息", errors.New("无法获取WebDav用户信息")
		}

		// result, err := pbDavConfig.NewPubDavConfigClient(d.HalalCommon.serv.GetGrpcConnection()).Get(ctx, &pbDavConfig.DavConfig{})
		// if err != nil {
		// 	return "无法获取Webdav信息", err
		// }

		// 构造基础URL
		baseURL := "https://dav.2dland.cn"
		// 解析基础URL
		parsedURL, err := url.Parse(baseURL)
		if err != nil {
			return "无法解析下载网址", err
		}

		// 设置用户名和密码
		username := d.WebDavUserName
		password := d.WebDavPassWord

		// 设置用户名和密码
		parsedURL.User = url.UserPassword(username, password)
		linkurl := parsedURL.String() + args.Obj.GetPath()

		return linkurl, nil
	}

	dataBytes, err := json.Marshal(args.Data)
	if err != nil {
		return nil, fmt.Errorf("解析data数据出错: %w ,注意data为json格式", err)
	}
	if string(dataBytes) == "null" || string(dataBytes) == "{}" || string(dataBytes) == "\"\"" {
		return nil, errors.New("data不能为空")
	}

	jsonStr := string(dataBytes)
	jsonStr = strings.ReplaceAll(jsonStr, "__FILEID__", args.Obj.GetID())
	jsonStr = strings.ReplaceAll(jsonStr, "__FILENAME__", args.Obj.GetName())
	jsonStr = strings.ReplaceAll(jsonStr, "__FILEPATH__", args.Path)

	// ✅ Step 1: 判断是否有 action 字段
	actionResult := gjson.Get(jsonStr, "action")
	if !actionResult.Exists() {
		return nil, errors.New("请传递action字段")
	}

	action := strings.ToUpper(actionResult.String())
	// 构造请求数据，由于2dland采用的不是http请求，因此不需要传递url，只需要传递method和body即可
	method := strings.ToUpper(gjson.Get(jsonStr, "method").String())
	body := gjson.Get(jsonStr, "body").Raw

	switch action {
	case "SHARE":
		if method == "" {
			method = "LIST" // 默认 GET
		}
		switch method {
		case "LIST":
			var shareRequestBody pubFileShare.FileShareListRequest
			err = json.Unmarshal([]byte(body), &shareRequestBody)
			if err != nil {
				// 不是 JSON，就直接返回原始字符串
				return nil, fmt.Errorf("body信息传递错误: %s", err.Error())
			}
			shares, err := pubFileShare.NewPubFileShareClient(d.HalalCommon.serv.GetGrpcConnection()).List(ctx, &shareRequestBody)
			if err != nil {
				// 不是 JSON，就直接返回原始字符串
				return nil, fmt.Errorf("获取分享列表失败: %s", err.Error())
			}
			return shares, nil

		case "CREATE":
			var createShareRequestBody pubFileShare.FileShare
			err = json.Unmarshal([]byte(body), &createShareRequestBody)
			if err != nil {
				// 不是 JSON，就直接返回原始字符串
				return nil, fmt.Errorf("body信息传递错误: %s", err.Error())
			}
			share, err := pubFileShare.NewPubFileShareClient(d.HalalCommon.serv.GetGrpcConnection()).Create(ctx, &createShareRequestBody)
			if err != nil {
				// 不是 JSON，就直接返回原始字符串
				return nil, fmt.Errorf("创建分享列表失败: %s", err.Error())
			}
			return share, nil

		case "GET":
			var getShareRequestBody pubFileShare.FileShare
			err = json.Unmarshal([]byte(body), &getShareRequestBody)
			if err != nil {
				// 不是 JSON，就直接返回原始字符串
				return nil, fmt.Errorf("body信息传递错误: %s", err.Error())
			}
			share, err := pubFileShare.NewPubFileShareClient(d.HalalCommon.serv.GetGrpcConnection()).Get(ctx, &getShareRequestBody)
			if err != nil {
				// 不是 JSON，就直接返回原始字符串
				return nil, fmt.Errorf("获取分享列表失败: %s", err.Error())
			}
			return share, nil

		case "LIKE":
			var shareRequestBody pubFileShare.FileShare
			err = json.Unmarshal([]byte(body), &shareRequestBody)
			if err != nil {
				// 不是 JSON，就直接返回原始字符串
				return nil, fmt.Errorf("body信息传递错误: %s", err.Error())
			}
			share, err := pubFileShare.NewPubFileShareClient(d.HalalCommon.serv.GetGrpcConnection()).Like(ctx, &shareRequestBody)
			if err != nil {
				// 不是 JSON，就直接返回原始字符串
				return nil, fmt.Errorf("获取分享列表失败: %s", err.Error())
			}
			return share, nil

		case "DISLIKE":
			var shareRequestBody pubFileShare.FileShare
			err = json.Unmarshal([]byte(body), &shareRequestBody)
			if err != nil {
				// 不是 JSON，就直接返回原始字符串
				return nil, fmt.Errorf("body信息传递错误: %s", err.Error())
			}
			share, err := pubFileShare.NewPubFileShareClient(d.HalalCommon.serv.GetGrpcConnection()).Dislike(ctx, &shareRequestBody)
			if err != nil {
				// 不是 JSON，就直接返回原始字符串
				return nil, fmt.Errorf("获取分享列表失败: %s", err.Error())
			}
			return share, nil

		case "SAVE":
			var shareRequestBody pubFileShare.FileShare
			err = json.Unmarshal([]byte(body), &shareRequestBody)
			if err != nil {
				// 不是 JSON，就直接返回原始字符串
				return nil, fmt.Errorf("body信息传递错误: %s", err.Error())
			}
			share, err := pubFileShare.NewPubFileShareClient(d.HalalCommon.serv.GetGrpcConnection()).Save(ctx, &shareRequestBody)
			if err != nil {
				// 不是 JSON，就直接返回原始字符串
				return nil, fmt.Errorf("获取分享列表失败: %s", err.Error())
			}
			return share, nil

		case "DELETE":
			var deleteShareRequestBody pubFileShare.FileShareDeleteRequest
			err = json.Unmarshal([]byte(body), &deleteShareRequestBody)
			if err != nil {
				// 不是 JSON，就直接返回原始字符串
				return nil, fmt.Errorf("body信息传递错误: %s", err.Error())
			}
			shares, err := pubFileShare.NewPubFileShareClient(d.HalalCommon.serv.GetGrpcConnection()).Delete(ctx, &deleteShareRequestBody)
			if err != nil {
				// 不是 JSON，就直接返回原始字符串
				return nil, fmt.Errorf("获取分享列表失败: %s", err.Error())
			}
			return shares, nil

		default:
			return nil, fmt.Errorf("未知的method类型: %s", method)
		}
	case "OFFLINE":
		if method == "" {
			method = "LIST" // 默认 GET
		}
		switch method {
		case "LIST":
			var offlineRequestBody pubUserOffline.OfflineTaskListRequest
			err = json.Unmarshal([]byte(body), &offlineRequestBody)
			if err != nil {
				// 不是 JSON，就直接返回原始字符串
				return nil, fmt.Errorf("body信息传递错误: %s", err.Error())
			}
			offlines, err := pubUserOffline.NewPubOfflineTaskClient(d.HalalCommon.serv.GetGrpcConnection()).List(ctx, &offlineRequestBody)
			if err != nil {
				// 不是 JSON，就直接返回原始字符串
				return nil, fmt.Errorf("获取分享列表失败: %s", err.Error())
			}
			return offlines, nil

		case "PARSE":
			var parseOfflineRequestBody pubUserOffline.TaskParseRequest
			err = json.Unmarshal([]byte(body), &parseOfflineRequestBody)
			if err != nil {
				// 不是 JSON，就直接返回原始字符串
				return nil, fmt.Errorf("body信息传递错误: %s", err.Error())
			}
			offline, err := pubUserOffline.NewPubOfflineTaskClient(d.HalalCommon.serv.GetGrpcConnection()).Parse(ctx, &parseOfflineRequestBody)
			if err != nil {
				// 不是 JSON，就直接返回原始字符串
				return nil, fmt.Errorf("获取分享列表失败: %s", err.Error())
			}
			return offline, nil

		case "ADD":
			var addOfflineRequestBody pubUserOffline.UserTask
			err = json.Unmarshal([]byte(body), &addOfflineRequestBody)
			if err != nil {
				// 不是 JSON，就直接返回原始字符串
				return nil, fmt.Errorf("body信息传递错误: %s", err.Error())
			}
			offline, err := pubUserOffline.NewPubOfflineTaskClient(d.HalalCommon.serv.GetGrpcConnection()).Add(ctx, &addOfflineRequestBody)
			if err != nil {
				// 不是 JSON，就直接返回原始字符串
				return nil, fmt.Errorf("获取分享列表失败: %s", err.Error())
			}
			return offline, nil

		case "DELETE":
			var deleteOfflineRequestBody pubUserOffline.OfflineTaskDeleteRequest
			err = json.Unmarshal([]byte(body), &deleteOfflineRequestBody)
			if err != nil {
				// 不是 JSON，就直接返回原始字符串
				return nil, fmt.Errorf("body信息传递错误: %s", err.Error())
			}
			offlines, err := pubUserOffline.NewPubOfflineTaskClient(d.HalalCommon.serv.GetGrpcConnection()).Delete(ctx, &deleteOfflineRequestBody)
			if err != nil {
				// 不是 JSON，就直接返回原始字符串
				return nil, fmt.Errorf("获取分享列表失败: %s", err.Error())
			}
			return offlines, nil

		default:
			return nil, fmt.Errorf("未知的method类型: %s", method)
		}
	default:
		return nil, fmt.Errorf("未知的action类型: %s", action)
	}

}

func (d *HalalCloud) put(ctx context.Context, dstDir model.Obj, fileStream model.FileStreamer, up driver.UpdateProgress) (model.Obj, error) {

	newDir := path.Join(dstDir.GetPath(), fileStream.GetName())

	result, err := pubUserFile.NewPubUserFileClient(d.HalalCommon.serv.GetGrpcConnection()).CreateUploadToken(ctx, &pubUserFile.File{
		Path: newDir,
	})
	if err != nil {
		return nil, err
	}
	u, _ := url.Parse(result.Endpoint)
	u.Host = "s3." + u.Host
	result.Endpoint = u.String()
	s, err := session.NewSession(&aws.Config{
		HTTPClient:       base.HttpClient,
		Credentials:      credentials.NewStaticCredentials(result.AccessKey, result.SecretKey, result.Token),
		Region:           aws.String(result.Region),
		Endpoint:         aws.String(result.Endpoint),
		S3ForcePathStyle: aws.Bool(true),
	})
	if err != nil {
		return nil, err
	}
	uploader := s3manager.NewUploader(s, func(u *s3manager.Uploader) {
		u.Concurrency = d.uploadThread
	})
	if fileStream.GetSize() > s3manager.MaxUploadParts*s3manager.DefaultUploadPartSize {
		uploader.PartSize = fileStream.GetSize() / (s3manager.MaxUploadParts - 1)
	}
	_, err = uploader.UploadWithContext(ctx, &s3manager.UploadInput{
		Bucket: aws.String(result.Bucket),
		Key:    aws.String(result.Key),
		Body:   io.TeeReader(fileStream, driver.NewProgress(fileStream.GetSize(), up)),
	})
	return nil, err

}

var _ driver.Driver = (*HalalCloud)(nil)
