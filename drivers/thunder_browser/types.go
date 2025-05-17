package thunder_browser

import (
	"fmt"
	"strconv"
	"time"

	"github.com/alist-org/alist/v3/internal/model"
	"github.com/alist-org/alist/v3/pkg/utils"
	hash_extend "github.com/alist-org/alist/v3/pkg/utils/hash"
)

type ErrResp struct {
	ErrorCode        int64  `json:"error_code"`
	ErrorMsg         string `json:"error"`
	ErrorDescription string `json:"error_description"`
	//	ErrorDetails   interface{} `json:"error_details"`
}

func (e *ErrResp) IsError() bool {
	return e.ErrorCode != 0 || e.ErrorMsg != "" || e.ErrorDescription != ""
}

func (e *ErrResp) Error() string {
	return fmt.Sprintf("ErrorCode: %d ,Error: %s ,ErrorDescription: %s ", e.ErrorCode, e.ErrorMsg, e.ErrorDescription)
}

/*
* 验证码Token
**/
type CaptchaTokenRequest struct {
	Action       string            `json:"action"`
	CaptchaToken string            `json:"captcha_token"`
	ClientID     string            `json:"client_id"`
	DeviceID     string            `json:"device_id"`
	Meta         map[string]string `json:"meta"`
	RedirectUri  string            `json:"redirect_uri"`
}

type CaptchaTokenResponse struct {
	CaptchaToken string `json:"captcha_token"`
	ExpiresIn    int64  `json:"expires_in"`
	Url          string `json:"url"`
}

/*
* 登录
**/
type TokenResp struct {
	TokenType    string `json:"token_type"`
	AccessToken  string `json:"access_token"`
	RefreshToken string `json:"refresh_token"`
	ExpiresIn    int64  `json:"expires_in"`

	Sub    string `json:"sub"`
	UserID string `json:"user_id"`

	Token string `json:"token"` // "超级保险箱" 访问Token
}

func (t *TokenResp) GetToken() string {
	return fmt.Sprint(t.TokenType, " ", t.AccessToken)
}

// GetSpaceToken 获取"超级保险箱" 访问Token
func (t *TokenResp) GetSpaceToken() string {
	return t.Token
}

type SignInRequest struct {
	CaptchaToken string `json:"captcha_token"`

	ClientID     string `json:"client_id"`
	ClientSecret string `json:"client_secret"`

	Username string `json:"username"`
	Password string `json:"password"`
}

/*
* 文件
**/
type FileList struct {
	Kind            string  `json:"kind"`
	NextPageToken   string  `json:"next_page_token"`
	Files           []Files `json:"files"`
	Version         string  `json:"version"`
	VersionOutdated bool    `json:"version_outdated"`
	FolderType      int8
}

type Link struct {
	URL    string    `json:"url"`
	Token  string    `json:"token"`
	Expire time.Time `json:"expire"`
	Type   string    `json:"type"`
}

var _ model.Obj = (*Files)(nil)

type Files struct {
	Kind     string `json:"kind"`
	ID       string `json:"id"`
	ParentID string `json:"parent_id"`
	Name     string `json:"name"`
	//UserID         string    `json:"user_id"`
	Size string `json:"size"`
	//Revision       string    `json:"revision"`
	//FileExtension  string    `json:"file_extension"`
	//MimeType       string    `json:"mime_type"`
	//Starred        bool      `json:"starred"`
	WebContentLink string     `json:"web_content_link"`
	CreatedTime    CustomTime `json:"created_time"`
	ModifiedTime   CustomTime `json:"modified_time"`
	IconLink       string     `json:"icon_link"`
	ThumbnailLink  string     `json:"thumbnail_link"`
	// Md5Checksum    string    `json:"md5_checksum"`
	Hash string `json:"hash"`
	// Links map[string]Link `json:"links"`
	// Phase string          `json:"phase"`
	// Audit struct {
	// 	Status  string `json:"status"`
	// 	Message string `json:"message"`
	// 	Title   string `json:"title"`
	// } `json:"audit"`
	Medias []struct {
		//Category       string `json:"category"`
		//IconLink       string `json:"icon_link"`
		//IsDefault      bool   `json:"is_default"`
		//IsOrigin       bool   `json:"is_origin"`
		//IsVisible      bool   `json:"is_visible"`
		Link Link `json:"link"`
		//MediaID        string `json:"media_id"`
		//MediaName      string `json:"media_name"`
		//NeedMoreQuota  bool   `json:"need_more_quota"`
		//Priority       int    `json:"priority"`
		//RedirectLink   string `json:"redirect_link"`
		//ResolutionName string `json:"resolution_name"`
		// Video          struct {
		// 	AudioCodec string `json:"audio_codec"`
		// 	BitRate    int    `json:"bit_rate"`
		// 	Duration   int    `json:"duration"`
		// 	FrameRate  int    `json:"frame_rate"`
		// 	Height     int    `json:"height"`
		// 	VideoCodec string `json:"video_codec"`
		// 	VideoType  string `json:"video_type"`
		// 	Width      int    `json:"width"`
		// } `json:"video"`
		// VipTypes []string `json:"vip_types"`
	} `json:"medias"`
	Trashed     bool   `json:"trashed"`
	DeleteTime  string `json:"delete_time"`
	OriginalURL string `json:"original_url"`
	//Params            struct{} `json:"params"`
	//OriginalFileIndex int    `json:"original_file_index"`
	//Space             string `json:"space"`
	//Apps              []interface{} `json:"apps"`
	//Writable   bool   `json:"writable"`
	FolderType string `json:"folder_type"`
	//Collection interface{} `json:"collection"`
	FileType int8
}

func (c *Files) GetHash() utils.HashInfo {
	return utils.NewHashInfo(hash_extend.GCID, c.Hash)
}

func (c *Files) GetSize() int64        { size, _ := strconv.ParseInt(c.Size, 10, 64); return size }
func (c *Files) GetName() string       { return c.Name }
func (c *Files) CreateTime() time.Time { return c.CreatedTime.Time }
func (c *Files) ModTime() time.Time    { return c.ModifiedTime.Time }
func (c *Files) IsDir() bool           { return c.Kind == FOLDER }
func (c *Files) GetID() string         { return c.ID }
func (c *Files) GetPath() string {
	// 对特殊文件进行特殊处理
	if c.FileType == ThunderDriveType {
		return ThunderDriveFileID
	} else if c.FileType == ThunderBrowserDriveSafeType {
		return ThunderBrowserDriveSafeFileID
	}
	return ""
}
func (c *Files) Thumb() string { return c.ThumbnailLink }

/*
* 上传
**/
type UploadTaskResponse struct {
	UploadType string `json:"upload_type"`

	/*//UPLOAD_TYPE_FORM
	Form struct {
		//Headers struct{} `json:"headers"`
		Kind       string `json:"kind"`
		Method     string `json:"method"`
		MultiParts struct {
			OSSAccessKeyID string `json:"OSSAccessKeyId"`
			Signature      string `json:"Signature"`
			Callback       string `json:"callback"`
			Key            string `json:"key"`
			Policy         string `json:"policy"`
			XUserData      string `json:"x:user_data"`
		} `json:"multi_parts"`
		URL string `json:"url"`
	} `json:"form"`*/

	//UPLOAD_TYPE_RESUMABLE
	Resumable struct {
		Kind   string `json:"kind"`
		Params struct {
			AccessKeyID     string    `json:"access_key_id"`
			AccessKeySecret string    `json:"access_key_secret"`
			Bucket          string    `json:"bucket"`
			Endpoint        string    `json:"endpoint"`
			Expiration      time.Time `json:"expiration"`
			Key             string    `json:"key"`
			SecurityToken   string    `json:"security_token"`
		} `json:"params"`
		Provider string `json:"provider"`
	} `json:"resumable"`

	File Files `json:"file"`
}

// Meta 结构体代表文件的 meta 数据
type ResourceMeta struct {
	Icon         string `json:"icon"`
	Status       string `json:"status,omitempty"`
	Hash         string `json:"hash,omitempty"`
	MimeType     string `json:"mime_type,omitempty"`
	URLTag       string `json:"url_tag,omitempty"`
	URL          string `json:"url,omitempty"`
	BTCreateTime string `json:"bt_create_time,omitempty"`
}

// Dir 结构体表示一个目录
type ResourceDir struct {
	NextPageToken string         `json:"next_page_token"`
	Resources     []ResourceFile `json:"resources"`
	PageSize      int            `json:"page_size"`
}

// ResourceFile 结构体表示一个文件或目录（资源项）
type ResourceFile struct {
	IsDir     bool         `json:"is_dir"`
	Dir       *ResourceDir `json:"dir,omitempty"` // 如果是目录，Dir 可能存在
	ID        string       `json:"id"`
	Resolver  string       `json:"resolver"`
	FileSize  string       `json:"file_size"`
	Meta      ResourceMeta `json:"meta"`
	ParentID  string       `json:"parent_id"`
	FileIndex int          `json:"file_index"`
	Name      string       `json:"name"`
	FileCount int          `json:"file_count"`
}

// ResourceList 结构体表示 list 字段（包含 next_page_token 和 resources）
type ResourceList struct {
	NextPageToken string         `json:"next_page_token"`
	Resources     []ResourceFile `json:"resources"`
	PageSize      int            `json:"page_size"`
}

// ResourceResponse 结构体表示顶级 JSON 数据结构
type ResourceResponse struct {
	ListID string       `json:"list_id"`
	List   ResourceList `json:"list"`
}

// ResourceResponse 结构体表示顶级 JSON 数据结构
type FileSearchResponse struct {
	ResourceExited bool    `json:"resource_existed"`
	Files          []Files `json:"files"`
	NextPageToken  string  `json:"next_page_token"`
	Unrecognized   bool    `json:"unrecognized"`
}

type Task struct {
	Params struct {
		FolderType   string `json:"folder_type"`
		PredictSpeed string `json:"predict_speed"`
		PredictType  string `json:"predict_type"`
	} `json:"params"`
	Statuses          []interface{} `json:"statuses"`
	UserID            string        `json:"user_id"`
	FileName          string        `json:"file_name"`
	FileID            string        `json:"file_id"`
	Kind              string        `json:"kind"`
	StatusSize        int           `json:"status_size"`
	ThirdTaskID       string        `json:"third_task_id"`
	Name              string        `json:"name"`
	Type              string        `json:"type"`
	Phase             string        `json:"phase"`
	Callback          string        `json:"callback"`
	ID                string        `json:"id"`
	Progress          int           `json:"progress"`
	IconLink          string        `json:"icon_link"`
	Message           string        `json:"message"`
	CreatedTime       time.Time     `json:"created_time"`
	ReferenceResource interface{}   `json:"reference_resource"`
	Space             string        `json:"space"`
	UpdatedTime       time.Time     `json:"updated_time"`
	FileSize          string        `json:"file_size"`
}

type URL struct {
	Kind string `json:"kind"`
}

type TaskResponse struct {
	Task       Task   `json:"task"`
	UploadType string `json:"upload_type"`
	URL        URL    `json:"url"`
	File       Files  `json:"file"`
}

// 定义 添加流畅播添加任务请求
type PlayURL struct {
	Files []string `json:"files"`
	URL   string   `json:"url"`
}

type PlayParams struct {
	Referer    string `json:"referer"`
	Played     string `json:"played"`
	DedupIndex string `json:"dedup_index"`
	Scene      string `json:"scene"`
	WebTitle   string `json:"web_title"`
}

type PlayRequest struct {
	Params     PlayParams `json:"params"`
	UploadType string     `json:"upload_type"`
	FolderType string     `json:"folder_type"`
	Space      string     `json:"space"`
	NeedDedup  bool       `json:"need_dedup"`
	Kind       string     `json:"kind"`
	Name       string     `json:"name"`
	URL        PlayURL    `json:"url"`
}
