package thunderx

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	neturl "net/url"
	"strings"

	"github.com/alist-org/alist/v3/drivers/base"
	"github.com/alist-org/alist/v3/internal/driver"
	"github.com/alist-org/alist/v3/internal/errs"
	"github.com/alist-org/alist/v3/internal/model"
	"github.com/alist-org/alist/v3/internal/op"
	"github.com/alist-org/alist/v3/pkg/utils"
	hash_extend "github.com/alist-org/alist/v3/pkg/utils/hash"
	"github.com/aws/aws-sdk-go/aws"
	"github.com/aws/aws-sdk-go/aws/credentials"
	"github.com/aws/aws-sdk-go/aws/session"
	"github.com/aws/aws-sdk-go/service/s3/s3manager"
	"github.com/go-resty/resty/v2"
	"github.com/tidwall/gjson"
)

type ThunderX struct {
	*XunLeiXCommon
	model.Storage
	Addition

	identity string
}

func (x *ThunderX) Config() driver.Config {
	return config
}

func (x *ThunderX) GetAddition() driver.Additional {
	return &x.Addition
}

func (x *ThunderX) Init(ctx context.Context) (err error) {
	// 初始化所需参数
	if x.XunLeiXCommon == nil {
		x.XunLeiXCommon = &XunLeiXCommon{
			Common: &Common{
				client:            base.NewRestyClient(),
				Algorithms:        Algorithms,
				DeviceID:          utils.GetMD5EncodeStr(x.Username + x.Password),
				ClientID:          ClientID,
				ClientSecret:      ClientSecret,
				ClientVersion:     ClientVersion,
				PackageName:       PackageName,
				UserAgent:         BuildCustomUserAgent(utils.GetMD5EncodeStr(x.Username+x.Password), ClientID, PackageName, SdkVersion, ClientVersion, PackageName, ""),
				DownloadUserAgent: DownloadUserAgent,
				UseVideoUrl:       x.UseVideoUrl,
				UseProxy:          x.UseProxy,
				//下载地址是否使用代理
				UseUrlProxy: x.UseUrlProxy,
				ProxyUrl:    x.ProxyUrl,

				refreshCTokenCk: func(token string) {
					x.CaptchaToken = token
					op.MustSaveDriverStorage(x)
				},
			},
			refreshTokenFunc: func() error {
				// 通过RefreshToken刷新
				token, err := x.RefreshToken(x.TokenResp.RefreshToken)
				if err != nil {
					// 重新登录
					token, err = x.Login(x.Username, x.Password)
					if err != nil {
						x.GetStorage().SetStatus(fmt.Sprintf("%+v", err.Error()))
						if token.UserID != "" {
							x.SetUserID(token.UserID)
							x.UserAgent = BuildCustomUserAgent(utils.GetMD5EncodeStr(x.Username+x.Password), ClientID, PackageName, SdkVersion, ClientVersion, PackageName, token.UserID)
						}
						op.MustSaveDriverStorage(x)
					}
				}
				x.SetTokenResp(token)
				return err
			},
		}
	}

	// 自定义验证码token
	ctoken := strings.TrimSpace(x.CaptchaToken)
	if ctoken != "" {
		x.SetCaptchaToken(ctoken)
	}
	if x.DeviceID == "" {
		x.SetDeviceID(utils.GetMD5EncodeStr(x.Username + x.Password))
	}

	x.XunLeiXCommon.UseVideoUrl = x.UseVideoUrl
	x.Addition.RootFolderID = x.RootFolderID
	// 防止重复登录
	identity := x.GetIdentity()
	if x.identity != identity || !x.IsLogin() {
		x.identity = identity
		// 登录
		token, err := x.Login(x.Username, x.Password)
		if err != nil {
			return err
		}
		x.SetTokenResp(token)
		if token.UserID != "" {
			x.SetUserID(token.UserID)
			x.UserAgent = BuildCustomUserAgent(x.DeviceID, ClientID, PackageName, SdkVersion, ClientVersion, PackageName, token.UserID)
		}
	}
	return nil
}

func (x *ThunderX) Drop(ctx context.Context) error {
	return nil
}

type ThunderXExpert struct {
	*XunLeiXCommon
	model.Storage
	ExpertAddition

	identity string
}

func (x *ThunderXExpert) Config() driver.Config {
	return configExpert
}

func (x *ThunderXExpert) GetAddition() driver.Additional {
	return &x.ExpertAddition
}

func (x *ThunderXExpert) Init(ctx context.Context) (err error) {
	// 防止重复登录
	identity := x.GetIdentity()
	if identity != x.identity || !x.IsLogin() {
		x.identity = identity
		x.XunLeiXCommon = &XunLeiXCommon{
			Common: &Common{
				client: base.NewRestyClient(),

				DeviceID: func() string {
					if len(x.DeviceID) != 32 {
						if x.LoginType == "user" {
							return utils.GetMD5EncodeStr(x.Username + x.Password)
						}
						return utils.GetMD5EncodeStr(x.ExpertAddition.RefreshToken)
					}
					return x.DeviceID
				}(),
				ClientID:      x.ClientID,
				ClientSecret:  x.ClientSecret,
				ClientVersion: x.ClientVersion,
				PackageName:   x.PackageName,
				UserAgent: func() string {
					if x.ExpertAddition.UserAgent != "" {
						return x.ExpertAddition.UserAgent
					}
					if x.LoginType == "user" {
						return BuildCustomUserAgent(utils.GetMD5EncodeStr(x.Username+x.Password), ClientID, PackageName, SdkVersion, ClientVersion, PackageName, "")
					}
					return BuildCustomUserAgent(utils.GetMD5EncodeStr(x.ExpertAddition.RefreshToken), ClientID, PackageName, SdkVersion, ClientVersion, PackageName, "")
				}(),
				DownloadUserAgent: func() string {
					if x.ExpertAddition.DownloadUserAgent != "" {
						return x.ExpertAddition.DownloadUserAgent
					}
					return DownloadUserAgent
				}(),
				UseVideoUrl: x.UseVideoUrl,
				UseProxy:    x.ExpertAddition.UseProxy,
				//下载地址是否使用代理
				UseUrlProxy: x.ExpertAddition.UseUrlProxy,
				ProxyUrl:    x.ExpertAddition.ProxyUrl,
				refreshCTokenCk: func(token string) {
					x.CaptchaToken = token
					op.MustSaveDriverStorage(x)
				},
			},
		}

		if x.ExpertAddition.CaptchaToken != "" {
			x.SetCaptchaToken(x.ExpertAddition.CaptchaToken)
			op.MustSaveDriverStorage(x)
		}
		if x.Common.DeviceID != "" {
			x.ExpertAddition.DeviceID = x.Common.DeviceID
			op.MustSaveDriverStorage(x)
		}
		if x.Common.DownloadUserAgent != "" {
			x.ExpertAddition.DownloadUserAgent = x.Common.DownloadUserAgent
			op.MustSaveDriverStorage(x)
		}
		x.XunLeiXCommon.UseVideoUrl = x.UseVideoUrl
		x.ExpertAddition.RootFolderID = x.RootFolderID
		// 签名方法
		if x.SignType == "captcha_sign" {
			x.Common.Timestamp = x.Timestamp
			x.Common.CaptchaSign = x.CaptchaSign
		} else {
			x.Common.Algorithms = strings.Split(x.Algorithms, ",")
		}

		// 登录方式
		if x.LoginType == "refresh_token" {
			// 通过RefreshToken登录
			token, err := x.XunLeiXCommon.RefreshToken(x.ExpertAddition.RefreshToken)
			if err != nil {
				return err
			}
			x.SetTokenResp(token)
			// 刷新token方法
			x.SetRefreshTokenFunc(func() error {
				token, err := x.XunLeiXCommon.RefreshToken(x.TokenResp.RefreshToken)
				if err != nil {
					x.GetStorage().SetStatus(fmt.Sprintf("%+v", err.Error()))
				}
				x.SetTokenResp(token)
				op.MustSaveDriverStorage(x)
				return err
			})
		} else {
			// 通过用户密码登录
			token, err := x.Login(x.Username, x.Password)
			if err != nil {
				return err
			}
			x.SetTokenResp(token)
			x.SetRefreshTokenFunc(func() error {
				token, err := x.XunLeiXCommon.RefreshToken(x.TokenResp.RefreshToken)
				if err != nil {
					token, err = x.Login(x.Username, x.Password)
					if err != nil {
						x.GetStorage().SetStatus(fmt.Sprintf("%+v", err.Error()))
					}
				}
				x.SetTokenResp(token)
				op.MustSaveDriverStorage(x)
				return err
			})
		}
		// 更新 UserAgent
		if x.TokenResp.UserID != "" {
			x.ExpertAddition.UserAgent = BuildCustomUserAgent(x.ExpertAddition.DeviceID, ClientID, PackageName, SdkVersion, ClientVersion, PackageName, x.TokenResp.UserID)
			x.SetUserAgent(x.ExpertAddition.UserAgent)
			op.MustSaveDriverStorage(x)
		}
	} else {
		// 仅修改验证码token
		if x.CaptchaToken != "" {
			x.SetCaptchaToken(x.CaptchaToken)
		}
		x.XunLeiXCommon.UserAgent = x.ExpertAddition.UserAgent
		x.XunLeiXCommon.DownloadUserAgent = x.ExpertAddition.UserAgent
		x.XunLeiXCommon.UseVideoUrl = x.UseVideoUrl
		x.ExpertAddition.RootFolderID = x.RootFolderID
	}
	return nil
}

func (x *ThunderXExpert) Drop(ctx context.Context) error {
	return nil
}

func (x *ThunderXExpert) SetTokenResp(token *TokenResp) {
	x.XunLeiXCommon.SetTokenResp(token)
	if token != nil {
		x.ExpertAddition.RefreshToken = token.RefreshToken
	}
}

type XunLeiXCommon struct {
	*Common
	*TokenResp // 登录信息

	refreshTokenFunc func() error
}

func (xc *XunLeiXCommon) List(ctx context.Context, dir model.Obj, args model.ListArgs) ([]model.Obj, error) {
	return xc.getFiles(ctx, dir.GetID())
}

func (xc *XunLeiXCommon) Link(ctx context.Context, file model.Obj, args model.LinkArgs) (*model.Link, error) {
	var lFile Files
	_, err := xc.Request(FILE_API_URL+"/{fileID}", http.MethodGet, func(r *resty.Request) {
		r.SetContext(ctx)
		r.SetPathParam("fileID", file.GetID())
		//r.SetQueryParam("space", "")
	}, &lFile)
	if err != nil {
		return nil, err
	}

	link := &model.Link{
		URL: lFile.WebContentLink,
		Header: http.Header{
			"User-Agent": {xc.DownloadUserAgent},
		},
	}

	if xc.UseVideoUrl {
		for _, media := range lFile.Medias {
			if media.Link.URL != "" {
				link.URL = media.Link.URL
				break
			}
		}
	}

	if xc.UseUrlProxy {
		if strings.HasSuffix(xc.ProxyUrl, "/") {
			link.URL = xc.ProxyUrl + link.URL
		} else {
			link.URL = xc.ProxyUrl + "/" + link.URL
		}
	}

	/*
		strs := regexp.MustCompile(`e=([0-9]*)`).FindStringSubmatch(lFile.WebContentLink)
		if len(strs) == 2 {
			timestamp, err := strconv.ParseInt(strs[1], 10, 64)
			if err == nil {
				expired := time.Duration(timestamp-time.Now().Unix()) * time.Second
				link.Expiration = &expired
			}
		}
	*/
	return link, nil
}

func (xc *XunLeiXCommon) MakeDir(ctx context.Context, parentDir model.Obj, dirName string) error {
	_, err := xc.Request(FILE_API_URL, http.MethodPost, func(r *resty.Request) {
		r.SetContext(ctx)
		r.SetBody(&base.Json{
			"kind":      FOLDER,
			"name":      dirName,
			"parent_id": parentDir.GetID(),
		})
	}, nil)
	return err
}

func (xc *XunLeiXCommon) Move(ctx context.Context, srcObj, dstDir model.Obj) error {
	_, err := xc.Request(FILE_API_URL+":batchMove", http.MethodPost, func(r *resty.Request) {
		r.SetContext(ctx)
		r.SetBody(&base.Json{
			"to":  base.Json{"parent_id": dstDir.GetID()},
			"ids": []string{srcObj.GetID()},
		})
	}, nil)
	return err
}

func (xc *XunLeiXCommon) Rename(ctx context.Context, srcObj model.Obj, newName string) error {
	_, err := xc.Request(FILE_API_URL+"/{fileID}", http.MethodPatch, func(r *resty.Request) {
		r.SetContext(ctx)
		r.SetPathParam("fileID", srcObj.GetID())
		r.SetBody(&base.Json{"name": newName})
	}, nil)
	return err
}

func (xc *XunLeiXCommon) Offline(ctx context.Context, args model.OtherArgs) (interface{}, error) {
	_, err := xc.Request(FILE_API_URL, http.MethodPost, func(r *resty.Request) {
		r.SetContext(ctx)
		r.SetHeaders(map[string]string{
			"X-Device-Id": xc.DeviceID,
			"User-Agent":  xc.UserAgent,
			"Peer-Id":     xc.DeviceID,
			"client_id":   xc.ClientID,
			"x-client-id": xc.ClientID,
			"X-Guid":      xc.DeviceID,
		})
		r.SetBody(&base.Json{
			"kind":        "drive#file",
			"name":        "",
			"parent_id":   args.Obj.GetID(),
			"upload_type": "UPLOAD_TYPE_URL",
			"url": &base.Json{
				"url":       args.Data,
				"params":    "{}",
				"parent_id": args.Obj.GetID(),
			},
		})
	}, nil)
	if err != nil {
		return nil, err
	}
	return "ok", nil
}

func (xc *XunLeiXCommon) Other(ctx context.Context, args model.OtherArgs) (interface{}, error) {

	if args.Data == nil {
		if args.Obj.IsDir() {
			return "无法获取文件夹的直链", errors.New("无法获取文件夹的直链")
		}

		var lFile Files
		_, err := xc.Request(FILE_API_URL+"/{fileID}", http.MethodGet, func(r *resty.Request) {
			r.SetContext(ctx)
			r.SetPathParam("fileID", args.Obj.GetID())
			//r.SetQueryParam("space", "")
		}, &lFile)
		if err != nil {
			return nil, err
		}

		link := &model.Link{
			URL: lFile.WebContentLink,
			Header: http.Header{
				"User-Agent": {xc.DownloadUserAgent},
			},
		}

		if xc.UseVideoUrl {
			for _, media := range lFile.Medias {
				if media.Link.URL != "" {
					link.URL = media.Link.URL
					break
				}
			}
		}
		return link.URL, nil
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

	action := actionResult.String()

	switch action {
	case "request":
		// 构造请求数据，假设传入的 json 里有 url, method, body 字段
		url := gjson.Get(jsonStr, "url").String()
		method := strings.ToUpper(gjson.Get(jsonStr, "method").String())
		body := gjson.Get(jsonStr, "body").Raw
		params := gjson.Get(jsonStr, "params").Raw

		if url == "" {
			return nil, errors.New("url不能为空")
		}
		if method == "" {
			method = "GET" // 默认 GET
		}
		paramValues := neturl.Values{}

		// 将 JSON 字符串 params 转换为 map[string]string
		if params != "" && params != "null" {
			var rawParams map[string]interface{}
			if err := json.Unmarshal([]byte(params), &rawParams); err != nil {
				return nil, errors.New("params 解析失败: " + err.Error())
			}
			for k, v := range rawParams {
				switch val := v.(type) {
				case string, float64, bool, int, int64, float32:
					paramValues.Add(k, utils.ConvertToString(val))
				case []interface{}:
					for _, item := range val {
						paramValues.Add(k, utils.ConvertToString(item))
					}
				case []string:
					for _, item := range val {
						paramValues.Add(k, item)
					}
				case map[string]interface{}:
					jsonStr, _ := json.Marshal(val)
					paramValues.Add(k, string(jsonStr))
				default:
					// 忽略不支持的类型
				}
			}
		}

		resp, err := xc.Request(url, method, func(r *resty.Request) {
			r.SetContext(ctx)
			r.SetHeaders(map[string]string{
				"X-Device-Id": xc.DeviceID,
				"User-Agent":  xc.UserAgent,
				"Peer-Id":     xc.DeviceID,
				"client_id":   xc.ClientID,
				"x-client-id": xc.ClientID,
				"X-Guid":      xc.DeviceID,
			})
			r.SetBody([]byte(body))
			if len(paramValues) > 0 {
				r.SetQueryParamsFromValues(paramValues)
			}
		}, nil)

		if err != nil {
			return nil, errors.New("请求错误")
		}

		var result interface{}
		err = json.Unmarshal(resp, &result)
		if err != nil {
			// 不是 JSON，就直接返回原始字符串
			return string(resp), nil
		}

		// 是合法 JSON，返回解析结果
		return result, nil

	default:
		return nil, fmt.Errorf("未知的action类型: %s", action)
	}
}

func (xc *XunLeiXCommon) Copy(ctx context.Context, srcObj, dstDir model.Obj) error {
	_, err := xc.Request(FILE_API_URL+":batchCopy", http.MethodPost, func(r *resty.Request) {
		r.SetContext(ctx)
		r.SetBody(&base.Json{
			"to":  base.Json{"parent_id": dstDir.GetID()},
			"ids": []string{srcObj.GetID()},
		})
	}, nil)
	return err
}

func (xc *XunLeiXCommon) Remove(ctx context.Context, obj model.Obj) error {
	// _, err := xc.Request(FILE_API_URL+"/{fileID}/trash", http.MethodPatch, func(r *resty.Request) {
	// 	r.SetContext(ctx)
	// 	r.SetPathParam("fileID", obj.GetID())
	// 	r.SetBody("{}")
	// }, nil)
	_, err := xc.Request(FILE_API_URL+":batchDelete", http.MethodPost, func(r *resty.Request) {
		r.SetContext(ctx)
		r.SetBody(&base.Json{
			"ids": []string{obj.GetID()},
		})
	}, nil)
	return err
}

func (xc *XunLeiXCommon) Put(ctx context.Context, dstDir model.Obj, stream model.FileStreamer, up driver.UpdateProgress) error {
	hi := stream.GetHash()
	gcid := hi.GetHash(hash_extend.GCID)
	if len(gcid) < hash_extend.GCID.Width {
		tFile, err := stream.CacheFullInTempFile()
		if err != nil {
			return err
		}

		gcid, err = utils.HashFile(hash_extend.GCID, tFile, stream.GetSize())
		if err != nil {
			return err
		}
	}

	var resp UploadTaskResponse
	_, err := xc.Request(FILE_API_URL, http.MethodPost, func(r *resty.Request) {
		r.SetContext(ctx)
		r.SetBody(&base.Json{
			"kind":        FILE,
			"parent_id":   dstDir.GetID(),
			"name":        stream.GetName(),
			"size":        stream.GetSize(),
			"hash":        gcid,
			"upload_type": UPLOAD_TYPE_RESUMABLE,
		})
	}, &resp)
	if err != nil {
		return err
	}

	param := resp.Resumable.Params
	if resp.UploadType == UPLOAD_TYPE_RESUMABLE {
		param.Endpoint = strings.TrimLeft(param.Endpoint, param.Bucket+".")
		s, err := session.NewSession(&aws.Config{
			Credentials: credentials.NewStaticCredentials(param.AccessKeyID, param.AccessKeySecret, param.SecurityToken),
			Region:      aws.String("xunlei"),
			Endpoint:    aws.String(param.Endpoint),
		})
		if err != nil {
			return err
		}
		uploader := s3manager.NewUploader(s)
		if stream.GetSize() > s3manager.MaxUploadParts*s3manager.DefaultUploadPartSize {
			uploader.PartSize = stream.GetSize() / (s3manager.MaxUploadParts - 1)
		}
		_, err = uploader.UploadWithContext(ctx, &s3manager.UploadInput{
			Bucket:  aws.String(param.Bucket),
			Key:     aws.String(param.Key),
			Expires: aws.Time(param.Expiration),
			Body:    stream,
		})
		return err
	}
	return nil
}

func (xc *XunLeiXCommon) getFiles(ctx context.Context, folderId string) ([]model.Obj, error) {
	files := make([]model.Obj, 0)
	var pageToken string
	for {
		var fileList FileList
		_, err := xc.Request(FILE_API_URL, http.MethodGet, func(r *resty.Request) {
			r.SetContext(ctx)
			r.SetQueryParams(map[string]string{
				"space":      "",
				"__type":     "drive",
				"refresh":    "true",
				"__sync":     "true",
				"parent_id":  folderId,
				"page_token": pageToken,
				"with_audit": "true",
				"limit":      "100",
				"filters":    `{"phase":{"eq":"PHASE_TYPE_COMPLETE"},"trashed":{"eq":false}}`,
			})
		}, &fileList)
		if err != nil {
			return nil, err
		}

		for i := 0; i < len(fileList.Files); i++ {
			files = append(files, &fileList.Files[i])
		}

		if fileList.NextPageToken == "" {
			break
		}
		pageToken = fileList.NextPageToken
	}
	return files, nil
}

// SetRefreshTokenFunc 设置刷新Token的方法
func (xc *XunLeiXCommon) SetRefreshTokenFunc(fn func() error) {
	xc.refreshTokenFunc = fn
}

// SetTokenResp 设置Token
func (xc *XunLeiXCommon) SetTokenResp(tr *TokenResp) {
	xc.TokenResp = tr
}

// Request 携带Authorization和CaptchaToken的请求
func (xc *XunLeiXCommon) Request(url string, method string, callback base.ReqCallback, resp interface{}) ([]byte, error) {
	data, err := xc.Common.Request(url, method, func(req *resty.Request) {
		req.SetHeaders(map[string]string{
			"Authorization":   xc.Token(),
			"X-Captcha-Token": xc.GetCaptchaToken(),
		})
		if callback != nil {
			callback(req)
		}
	}, resp)

	errResp, ok := err.(*ErrResp)

	if !ok {
		if err != nil {
			return nil, err
		}
		return data, nil
	}

	switch errResp.ErrorCode {
	case 0:
		return data, nil
	case 4122, 4121, 10, 16:
		if xc.refreshTokenFunc != nil {
			if err = xc.refreshTokenFunc(); err == nil {
				break
			}
		}
		return nil, err
	case 9: // 验证码token过期
		if err = xc.RefreshCaptchaTokenAtLogin(GetAction(method, url), xc.UserID); err != nil {
			return nil, err
		}
	default:
		return nil, err
	}
	return xc.Request(url, method, callback, resp)
}

// RefreshToken 刷新Token
func (xc *XunLeiXCommon) RefreshToken(refreshToken string) (*TokenResp, error) {
	var resp TokenResp
	_, err := xc.Common.Request(XLUSER_API_URL+"/auth/token", http.MethodPost, func(req *resty.Request) {
		req.SetBody(&base.Json{
			"grant_type":    "refresh_token",
			"refresh_token": refreshToken,
			"client_id":     xc.ClientID,
			"client_secret": xc.ClientSecret,
		})
	}, &resp)
	if err != nil {
		return nil, err
	}

	if resp.RefreshToken == "" {
		return nil, errs.EmptyToken
	}
	resp.UserID = resp.Sub
	return &resp, nil
}

// Login 登录
func (xc *XunLeiXCommon) Login(username, password string) (*TokenResp, error) {
	url := XLUSER_API_URL + "/auth/signin"
	err := xc.RefreshCaptchaTokenInLogin(GetAction(http.MethodPost, url), username)
	if err != nil {
		return nil, err
	}

	var resp TokenResp
	_, err = xc.Common.Request(url, http.MethodPost, func(req *resty.Request) {
		req.SetBody(&SignInRequest{
			CaptchaToken: xc.GetCaptchaToken(),
			ClientID:     xc.ClientID,
			ClientSecret: xc.ClientSecret,
			Username:     username,
			Password:     password,
		})
	}, &resp)
	if err != nil {
		return nil, err
	}
	resp.UserID = resp.Sub
	return &resp, nil
}

func (xc *XunLeiXCommon) IsLogin() bool {
	if xc.TokenResp == nil {
		return false
	}
	_, err := xc.Request(XLUSER_API_URL+"/user/me", http.MethodGet, nil, nil)
	return err == nil
}
