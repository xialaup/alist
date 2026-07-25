package halalcloud_open

import (
	"sync"
	"time"

	sdkUser "github.com/halalcloud/golang-sdk-lite/halalcloud/services/user"
)

var (
	slicePostErrorRetryInterval = time.Second * 120
	retryTimes                  = 5
)

type halalCommon struct {
	UserInfo         *sdkUser.User
	refreshTokenFunc func(token string) error
	configs          sync.Map
}

func (m *halalCommon) GetAccessToken() (string, error) {
	value, exists := m.configs.Load("access_token")
	if !exists {
		return "", nil
	}
	return value.(string), nil
}

func (m *halalCommon) GetRefreshToken() (string, error) {
	value, exists := m.configs.Load("refresh_token")
	if !exists {
		return "", nil
	}
	return value.(string), nil
}

func (m *halalCommon) SetAccessToken(token string) error {
	m.configs.Store("access_token", token)
	return nil
}

func (m *halalCommon) SetRefreshToken(token string) error {
	m.configs.Store("refresh_token", token)
	if m.refreshTokenFunc != nil {
		return m.refreshTokenFunc(token)
	}
	return nil
}

func (m *halalCommon) SetToken(accessToken string, refreshToken string, expiresIn int64) error {
	m.configs.Store("access_token", accessToken)
	m.configs.Store("refresh_token", refreshToken)
	m.configs.Store("expires_in", expiresIn)
	if m.refreshTokenFunc != nil {
		return m.refreshTokenFunc(refreshToken)
	}
	return nil
}

func (m *halalCommon) ClearConfigs() error {
	m.configs = sync.Map{}
	return nil
}

func (m *halalCommon) DeleteConfig(key string) error {
	m.configs.Delete(key)
	return nil
}

func (m *halalCommon) GetConfig(key string) (string, error) {
	value, exists := m.configs.Load(key)
	if !exists {
		return "", nil
	}
	return value.(string), nil
}

func (m *halalCommon) ListConfigs() (map[string]string, error) {
	configs := make(map[string]string)
	m.configs.Range(func(key, value interface{}) bool {
		configs[key.(string)] = value.(string)
		return true
	})
	return configs, nil
}

func (m *halalCommon) SetConfig(key string, value string) error {
	m.configs.Store(key, value)
	return nil
}
