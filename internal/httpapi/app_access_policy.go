package httpapi

import (
	"fmt"
	"strings"

	"golang.org/x/crypto/bcrypt"
)

type appAccessSettingsRequest struct {
	PasswordEnabled bool   `json:"passwordEnabled"`
	Username        string `json:"username"`
	Password        string `json:"password"`
}

type appAccessPolicy struct {
	Enabled  bool
	Username string
	Hash     string
}

func resolveAppAccessPolicy(request *appAccessSettingsRequest, current appAccessPolicy) (appAccessPolicy, error) {
	if request == nil {
		return current, nil
	}
	if !request.PasswordEnabled {
		return appAccessPolicy{}, nil
	}
	username := strings.TrimSpace(request.Username)
	if username == "" || len([]rune(username)) > 64 {
		return appAccessPolicy{}, fmt.Errorf("密码访问用户名必须为 1 到 64 个字符")
	}
	password := request.Password
	hash := current.Hash
	if password != "" {
		if len(password) < 8 {
			return appAccessPolicy{}, fmt.Errorf("密码访问的密码至少需要 8 个字符")
		}
		if len([]byte(password)) > 72 {
			return appAccessPolicy{}, fmt.Errorf("密码访问的密码不能超过 72 字节")
		}
		encoded, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.DefaultCost)
		if err != nil {
			return appAccessPolicy{}, err
		}
		hash = string(encoded)
	}
	if hash == "" {
		return appAccessPolicy{}, fmt.Errorf("首次开启密码访问时必须设置密码")
	}
	return appAccessPolicy{Enabled: true, Username: username, Hash: hash}, nil
}
