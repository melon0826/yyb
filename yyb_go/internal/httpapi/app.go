package httpapi

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/gin-gonic/gin"
	httpSwagger "github.com/swaggo/http-swagger/v2"

	"yyb_go/internal/protocol"
	"yyb_go/internal/qr"
	"yyb_go/internal/store"
)

type Config struct {
	ResourceRoot   string
	DBFilename     string
	TCPProxy       string
	SessionTTL     time.Duration
	RequestTimeout time.Duration
	AvatarTimeout  time.Duration
	ScanTimeout    time.Duration
	QRSessionTTL   time.Duration
}

type App struct {
	cfg       Config
	resources resources
	db        *store.DB
	pool      *protocol.Pool
	qr        *qr.Client

	mu         sync.Mutex
	qrSessions map[string]*qr.Session
}

var swaggerDocsHandler = httpSwagger.Handler(
	httpSwagger.URL("/openapi.json"),
	httpSwagger.DocExpansion("list"),
	httpSwagger.DeepLinking(true),
	httpSwagger.DefaultModelsExpandDepth(httpSwagger.ShowModel),
)

func NewApp(cfg Config) (*App, error) {
	if cfg.ResourceRoot == "" {
		cfg.ResourceRoot = filepath.Join(".", "resource")
	}
	if cfg.DBFilename == "" {
		cfg.DBFilename = DefaultDBFilename
	}
	if cfg.RequestTimeout == 0 {
		cfg.RequestTimeout = 8 * time.Second
	}
	if cfg.AvatarTimeout == 0 {
		cfg.AvatarTimeout = 10 * time.Second
	}
	if cfg.SessionTTL == 0 {
		cfg.SessionTTL = 30 * time.Minute
	}
	if cfg.QRSessionTTL == 0 {
		cfg.QRSessionTTL = 5 * time.Minute
	}
	res, err := ensureResources(cfg.ResourceRoot)
	if err != nil {
		return nil, err
	}
	dbPath, err := prepareDBPath(res.DB, cfg.DBFilename)
	if err != nil {
		return nil, err
	}
	db, err := store.Open(dbPath)
	if err != nil {
		return nil, err
	}
	poolCfg := protocol.DefaultConfig()
	poolCfg.SessionTTL = cfg.SessionTTL
	poolCfg.ShortlinkTimeout = cfg.RequestTimeout
	poolCfg.TCPProxy = cfg.TCPProxy
	pool := protocol.NewPool(poolCfg, db)
	return &App{
		cfg:        cfg,
		resources:  res,
		db:         db,
		pool:       pool,
		qr:         qr.NewClient(cfg.RequestTimeout),
		qrSessions: map[string]*qr.Session{},
	}, nil
}

func (a *App) Close() error {
	if a.db != nil {
		return a.db.Close()
	}
	return nil
}

func (a *App) Handler() http.Handler {
	if os.Getenv(gin.EnvGinMode) == "" {
		gin.SetMode(gin.ReleaseMode)
	}

	router := gin.New()
	router.Use(gin.Logger(), gin.Recovery())

	guard := newAuthGuard()
	router.Use(guard.middleware())

	router.Any("/", gin.WrapF(a.handleIndex))
	router.Any("/scan", gin.WrapF(a.handleScan))
	router.Any("/docs", func(c *gin.Context) {
		c.Redirect(http.StatusMovedPermanently, "/docs/index.html")
	})
	router.Any("/docs/*path", gin.WrapF(a.handleDocs))
	router.Any("/openapi.json", gin.WrapF(a.handleOpenAPI))
	router.Any("/health", func(c *gin.Context) {
		writeJSON(c.Writer, http.StatusOK, gin.H{"ok": true})
	})
	router.Any("/api/dashboard", gin.WrapF(a.handleDashboard))
	router.StaticFS("/static", http.Dir(a.resources.Static))
	router.Any("/qr", gin.WrapF(a.handleQRRoot))
	router.Any("/qr/*path", gin.WrapF(a.handleQR))
	router.Any("/accounts", gin.WrapF(a.handleAccountsRoot))
	router.Any("/accounts/avatar", gin.WrapF(a.handleAccountAvatar))
	router.Any("/accounts/refresh", gin.WrapF(a.handleAccountRefresh))
	router.Any("/accounts/resync", gin.WrapF(a.handleAccountResync))
	router.Any("/accounts/proxy", gin.WrapF(a.handleAccountProxy))
	router.Any("/accounts/disable", gin.WrapF(a.handleAccountDisable))
	router.Any("/accounts/remark", gin.WrapF(a.handleAccountRemark))
	router.Any("/accounts/status", gin.WrapF(a.handleAccountStatus))
	router.Any("/wxapp/getCode", gin.WrapF(a.handleGetCode))
	router.Any("/wxapp/getPhoneNumber", gin.WrapF(a.handleGetPhoneNumber))
	router.Any("/wx/getphonenumber", gin.WrapF(a.handleWxGetPhoneNumber))
	router.Any("/wxapp/operateWxData", gin.WrapF(a.handleOperateWXData))
	router.Any("/wxapp/checktoken", gin.WrapF(a.handleCheckToken))
	router.Any("/wx/code", gin.WrapF(a.handleWxCodeCompat))
	router.Any("/wx/getuserinfo", gin.WrapF(a.handleWxGetUserInfo))
	router.Any("/wx/encryptkey", gin.WrapF(a.handleWxEncryptKey))
	router.Any("/wx/oauth", gin.WrapF(a.handleWxOAuth))
	router.Any("/wx/autoauth", gin.WrapF(a.handleWxAutoOAuth))
	router.Any("/wx/heart", gin.WrapF(a.handleWxHeart))
	router.Any("/wx/qrcodeauth", gin.WrapF(a.handleWxQRCodeAuth))
	router.Any("/wx/call/init", gin.WrapF(a.handleWxCallInit))
	router.Any("/wx/cloud/call", gin.WrapF(a.handleWxCloudCall))
	router.Any("/wx/appmsgext", gin.WrapF(a.handleWxAppMsgExt))
	router.Any("/wx/appmsglike", gin.WrapF(a.handleWxAppMsgLike))
	router.Any("/api/proxies", gin.WrapF(a.handleProxiesDispatcher))
	router.Any("/api/audit-logs", gin.WrapF(a.handleListAuditLogs))
	router.Any("/accounts/batch", gin.WrapF(a.handleAccountBatch))
	router.NoRoute(func(c *gin.Context) {
		writeError(c.Writer, http.StatusNotFound, "not found")
	})

	return router
}

func (a *App) handleIndex(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/" {
		writeError(w, http.StatusNotFound, "not found")
		return
	}
	if r.Method != http.MethodGet {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	serveFileOrText(w, r, filepath.Join(a.resources.Templates, "index.html"), fallbackIndexHTML)
}

func (a *App) handleScan(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	serveFileOrText(w, r, filepath.Join(a.resources.Templates, "scan.html"), fallbackScanHTML)
}

func (a *App) handleDocs(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	if r.URL.Path == "/docs/" {
		http.Redirect(w, r, "/docs/index.html", http.StatusMovedPermanently)
		return
	}
	swaggerDocsHandler.ServeHTTP(w, r)
}

func (a *App) handleOpenAPI(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	writeRawJSON(w, http.StatusOK, openAPISpec)
}

func (a *App) handleQRRoot(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/qr" {
		writeError(w, http.StatusNotFound, "qr session not found")
		return
	}
	if r.Method != http.MethodPost {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	a.pruneQR()
	ctx, cancel := context.WithTimeout(r.Context(), a.cfg.RequestTimeout+35*time.Second)
	defer cancel()
	img, err := a.qr.GetQRCodeImage(ctx)
	if err != nil {
		writeError(w, http.StatusBadGateway, err.Error())
		return
	}
	a.mu.Lock()
	a.qrSessions[img.Session.ID] = img.Session
	keep := make(map[string]bool, len(a.qrSessions))
	for sid := range a.qrSessions {
		keep[sid] = true
	}
	a.mu.Unlock()
	path := a.resources.qrPath(img.Session.ID)
	_ = os.WriteFile(path, img.ImageBytes, 0o644)
	a.cleanupQR(keep)
	out := map[string]any{
		"session_id": img.Session.ID,
		"status":     img.Session.Status,
		"image_url":  "/qr/" + img.Session.ID + "/image",
	}
	if r.URL.Query().Get("as_base64") == "true" {
		out["image_base64"] = qr.DataURIJPEG(img.ImageBytes)
	} else {
		out["image_base64"] = nil
	}
	writeJSON(w, http.StatusOK, out)
}

func (a *App) handleQR(w http.ResponseWriter, r *http.Request) {
	parts := strings.Split(strings.TrimPrefix(r.URL.Path, "/qr/"), "/")
	if len(parts) != 2 {
		writeError(w, http.StatusNotFound, "qr session not found")
		return
	}
	sessionID, action := parts[0], parts[1]
	switch action {
	case "image":
		if r.Method != http.MethodGet {
			writeError(w, http.StatusMethodNotAllowed, "method not allowed")
			return
		}
		path := a.resources.qrPath(sessionID)
		if _, err := os.Stat(path); err != nil {
			writeError(w, http.StatusNotFound, "qr session not found")
			return
		}
		w.Header().Set("Content-Type", "image/jpeg")
		http.ServeFile(w, r, path)
	case "poll":
		if r.Method != http.MethodGet {
			writeError(w, http.StatusMethodNotAllowed, "method not allowed")
			return
		}
		sess := a.getQRSession(sessionID)
		if sess == nil {
			writeError(w, http.StatusNotFound, "qr session not found")
			return
		}
		result, err := a.qr.PollQRCode(r.Context(), sess)
		if err != nil {
			writeError(w, http.StatusBadGateway, err.Error())
			return
		}
		if terminalQR(result.Status) {
			a.dropQRSession(sessionID)
		}
		writeJSON(w, http.StatusOK, result)
	case "confirm":
		if r.Method != http.MethodPost {
			writeError(w, http.StatusMethodNotAllowed, "method not allowed")
			return
		}
		sess := a.getQRSession(sessionID)
		if sess == nil {
			writeError(w, http.StatusNotFound, "qr session not found")
			return
		}
		result, err := a.qr.GetLoginBuffer(r.Context(), sess)
		if err != nil {
			writeError(w, http.StatusConflict, "buffer not ready: "+err.Error())
			return
		}
		var userInfo map[string]any
		if ui, err := a.qr.LoginBuffers().FetchUserInfo(r.Context(), result.Credentials); err == nil {
			userInfo = ui
		}
		acc, err := a.storeFromScan(r.Context(), result.LoginBuffer, result.Credentials, userInfo)
		if err != nil {
			writeError(w, http.StatusInternalServerError, err.Error())
			return
		}
		a.dropQRSession(sessionID)
		writeJSON(w, http.StatusOK, acc.Public())
	default:
		writeError(w, http.StatusNotFound, "qr session not found")
	}
}

func (a *App) handleAccountsRoot(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/accounts" {
		writeError(w, http.StatusNotFound, "not found")
		return
	}
	switch r.Method {
	case http.MethodGet:
		accounts, err := a.db.ListAccounts(r.Context())
		if err != nil {
			writeError(w, http.StatusInternalServerError, err.Error())
			return
		}
		out := make([]store.AccountPublic, 0, len(accounts))
		for _, acc := range accounts {
			out = append(out, acc.Public())
		}
		writeJSON(w, http.StatusOK, out)
	case http.MethodDelete:
		acc, ok := a.resolveAccountFromQuery(w, r)
		if !ok {
			return
		}
		if err := a.db.DeleteAccount(r.Context(), acc.ID); err != nil {
			writeError(w, http.StatusInternalServerError, err.Error())
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"deleted": acc.ID, "openid": acc.OpenID})
	default:
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
	}
}

func (a *App) handleAccountAvatar(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/accounts/avatar" {
		writeError(w, http.StatusNotFound, "not found")
		return
	}
	if r.Method != http.MethodGet {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	acc, ok := a.resolveAccountFromQuery(w, r)
	if !ok {
		return
	}
	a.serveAvatar(w, r, acc)
}

func (a *App) handleAccountRefresh(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/accounts/refresh" {
		writeError(w, http.StatusNotFound, "not found")
		return
	}
	if r.Method != http.MethodPost {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	var body accountRefIn
	if err := decodeOptionalJSON(r, &body); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON: "+err.Error())
		return
	}
	if body.Ref == "" {
		// 兼容旧脚本用 openid 传账号
		body.Ref = strings.TrimSpace(r.URL.Query().Get("openid"))
		if body.Ref == "" && r.Method == http.MethodPost {
			var alt struct {
				OpenID string `json:"openid"`
			}
			if decodeOptionalJSON(r, &alt) == nil && alt.OpenID != "" {
				body.Ref = alt.OpenID
				// 重新解析完整 body
				_ = decodeOptionalJSON(r, &body)
			}
		}
	}
	if body.Ref == "" {
		writeError(w, http.StatusBadRequest, "ref is required")
		return
	}
	acc, ok := a.resolveAccountRef(w, r, body.Ref)
	if !ok {
		return
	}
	status := a.refreshLiveness(r.Context(), acc)
	writeJSON(w, http.StatusOK, refreshOut(acc, status))
}

func (a *App) handleAccountResync(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/accounts/resync" {
		writeError(w, http.StatusNotFound, "not found")
		return
	}
	if r.Method != http.MethodPost {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	var body accountRefIn
	if err := decodeOptionalJSON(r, &body); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON: "+err.Error())
		return
	}
	if body.Ref == "" {
		a.resyncAll(w, r)
		return
	}
	acc, ok := a.resolveAccountRef(w, r, body.Ref)
	if !ok {
		return
	}
	updated, err := a.resyncProfile(r.Context(), acc)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, updated.Public())
}

func (a *App) handleAccountProxy(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/accounts/proxy" {
		writeError(w, http.StatusNotFound, "not found")
		return
	}
	switch r.Method {
	case http.MethodPut:
		acc, ok := a.resolveAccountFromQuery(w, r)
		if !ok {
			return
		}
		var body struct {
			BoundProxy string `json:"bound_proxy"`
		}
		if err := decodeOptionalJSON(r, &body); err != nil {
			writeError(w, http.StatusBadRequest, "invalid JSON: "+err.Error())
			return
		}
		if err := a.db.SetAccountBoundProxy(r.Context(), acc.ID, body.BoundProxy); err != nil {
			writeError(w, http.StatusInternalServerError, err.Error())
			return
		}
		updated, err := a.db.GetAccount(r.Context(), acc.ID)
		if err != nil {
			writeError(w, http.StatusInternalServerError, err.Error())
			return
		}
		writeJSON(w, http.StatusOK, updated.Public())
	case http.MethodGet:
		acc, ok := a.resolveAccountFromQuery(w, r)
		if !ok {
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"bound_proxy": acc.BoundProxy, "openid": acc.OpenID})
	default:
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
	}
}

func (a *App) handleAccountDisable(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/accounts/disable" {
		writeError(w, http.StatusNotFound, "not found")
		return
	}
	if r.Method != http.MethodPost {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	var body struct {
		OpenID   string `json:"openid"`
		Disabled bool   `json:"disabled"`
	}
	if err := decodeOptionalJSON(r, &body); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON: "+err.Error())
		return
	}
	if body.OpenID == "" {
		writeError(w, http.StatusBadRequest, "openid is required")
		return
	}
	acc, ok := a.resolveAccountRef(w, r, body.OpenID)
	if !ok {
		return
	}
	if err := a.db.SetAccountDisabled(r.Context(), acc.ID, body.Disabled); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	updated, err := a.db.GetAccount(r.Context(), acc.ID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"account": updated.Public()})
}

func (a *App) handleAccountRemark(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/accounts/remark" {
		writeError(w, http.StatusNotFound, "not found")
		return
	}
	if r.Method != http.MethodPost {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	var body struct {
		OpenID      string `json:"openid"`
		DisplayName string `json:"displayName"`
	}
	if err := decodeOptionalJSON(r, &body); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON: "+err.Error())
		return
	}
	if body.OpenID == "" {
		writeError(w, http.StatusBadRequest, "openid is required")
		return
	}
	acc, ok := a.resolveAccountRef(w, r, body.OpenID)
	if !ok {
		return
	}
	if err := a.db.SetAccountAlias(r.Context(), acc.ID, body.DisplayName); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	updated, err := a.db.GetAccount(r.Context(), acc.ID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"account": updated.Public()})
}

func (a *App) handleAccountStatus(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/accounts/status" {
		writeError(w, http.StatusNotFound, "not found")
		return
	}
	if r.Method != http.MethodPost {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	var body struct {
		OpenID []string `json:"openid"`
	}
	if err := decodeOptionalJSON(r, &body); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON: "+err.Error())
		return
	}
	if len(body.OpenID) == 0 {
		writeError(w, http.StatusBadRequest, "openid is required")
		return
	}
	accounts, err := a.db.BatchAccountStatus(r.Context(), body.OpenID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, accounts)
}

func (a *App) handleGetCode(w http.ResponseWriter, r *http.Request) {
	if !acceptWXAppRoute(w, r, "/wxapp/getCode") {
		return
	}
	a.callWXApp(w, r, false, a.invokeGetCode)
}

// handleWxCodeCompat 兼容旧版 wx_server 的 /wx/code 接口格式
// 请求: POST /wx/code  body: { appid, openid }  headers: { auth(忽略) }
// 响应: { code:0, data:{ code:"xxx" } }
func (a *App) handleWxCodeCompat(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/wx/code" {
		writeError(w, http.StatusNotFound, "not found")
		return
	}
	if r.Method != http.MethodPost {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	var body struct {
		AppID  string `json:"appid"`
		OpenID string `json:"openid"`
	}
	if err := decodeOptionalJSON(r, &body); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON: "+err.Error())
		return
	}
	if body.OpenID == "" {
		writeError(w, http.StatusBadRequest, "openid is required")
		return
	}
	if body.AppID == "" {
		writeError(w, http.StatusBadRequest, "appid is required")
		return
	}
	acc, ok := a.resolveAccountRef(w, r, body.OpenID)
	if !ok {
		return
	}
	effectiveProxy := resolveEffectiveProxy(acc.BoundProxy, a.cfg.TCPProxy)
	result, err := a.invokeWXApp(r.Context(), acc, body.AppID, effectiveProxy, nil, func(ctx context.Context, acc *store.WechatAccount, appID string, proxy string, _ map[string]any) (map[string]any, error) {
		return a.pool.GetCode(ctx, acc.LoginBuffer, appID, acc.ID, proxy)
	})
	if err != nil {
		var expired accountExpiredError
		if errors.As(err, &expired) {
			writeError(w, http.StatusConflict, "account login_buffer expired; re-scan required")
		} else {
			writeError(w, http.StatusBadGateway, "get code failed: "+err.Error())
		}
		return
	}
	// 兼容旧格式: { code:0, data:{ code:"xxx" } }
	writeJSON(w, http.StatusOK, map[string]any{"code": result["code"]})
}

// handleWxGetUserInfo 处理微信用户信息获取请求 - 支持 encryptedData 解密
// POST /wx/getuserinfo  body: { ref, app_id, encrypted_data, iv }
func (a *App) handleWxGetUserInfo(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/wx/getuserinfo" {
		writeError(w, http.StatusNotFound, "not found")
		return
	}
	if r.Method != http.MethodPost {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	var body struct {
		Ref           string `json:"ref"`
		OpenID        string `json:"openid"`
		AppID         string `json:"app_id"`
		AppIDAlt      string `json:"appid"`
		EncryptedData string `json:"encrypted_data"`
		IV            string `json:"iv"`
	}
	if err := decodeOptionalJSON(r, &body); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON: "+err.Error())
		return
	}
	if body.Ref == "" {
		body.Ref = body.OpenID
	}
	if body.Ref == "" {
		writeError(w, http.StatusBadRequest, "ref is required")
		return
	}
	if body.AppID == "" {
		body.AppID = body.AppIDAlt
	}
	acc, ok := a.resolveAccountRef(w, r, body.Ref)
	if !ok {
		return
	}
	resp := map[string]any{
		"openid": acc.OpenID,
	}
	if acc.Credentials != nil {
		if sk := stringFromAny(acc.Credentials["session_key"]); sk != "" {
			resp["session_key"] = sk
		}
	}
	if acc.Nickname != nil {
		resp["nickname"] = *acc.Nickname
	}
	if acc.Avatar != nil {
		resp["avatar"] = *acc.Avatar
	}
	if acc.UserInfo != nil {
		resp["status"] = "full"
		tryPut(&resp, acc.UserInfo, "nickname", "nick_name")
		tryPut(&resp, acc.UserInfo, "avatar", "head_img_url")
	}
	// 如果提供了 encryptedData+iv+appID，通过 iLink 解密
	if body.AppID != "" && acc.LoginBuffer != "" && body.EncryptedData != "" && body.IV != "" {
		effectiveProxy := resolveEffectiveProxy(acc.BoundProxy, a.cfg.TCPProxy)
		decryptPayload := map[string]any{
			"api_name":        "getuserinfo",
			"encrypted_data":  body.EncryptedData,
			"iv":              body.IV,
		}
		result, err := a.invokeWXApp(r.Context(), acc, body.AppID, effectiveProxy, decryptPayload, func(ctx context.Context, acc *store.WechatAccount, appID string, proxy string, p map[string]any) (map[string]any, error) {
			return a.pool.OperateWXData(ctx, acc.LoginBuffer, appID, decryptPayload, acc.ID, proxy)
		})
		if err == nil && result != nil {
			tryPut(&resp, result, "nickname", "nickName", "nick_name")
			tryPut(&resp, result, "avatar", "avatarUrl", "head_img_url")
			if resp["nickname"] != nil || resp["avatar"] != nil {
				resp["status"] = "decrypted"
			}
		}
	} else if body.AppID != "" {
		effectiveProxy := resolveEffectiveProxy(acc.BoundProxy, a.cfg.TCPProxy)
		result, err := a.invokeWXApp(r.Context(), acc, body.AppID, effectiveProxy, nil, func(ctx context.Context, acc *store.WechatAccount, appID string, proxy string, _ map[string]any) (map[string]any, error) {
			return a.pool.GetCode(ctx, acc.LoginBuffer, appID, acc.ID, proxy)
		})
		if err == nil {
			resp["code"] = result["code"]
		}
		// ctrip/iqoo/haitian/fuyouhui/yichengtong 等脚本需要 encryptedData+iv
		// 通过 OperateWXData 的 getUserInfo 获取
		if acc.LoginBuffer != "" {
			getInfoPayload := map[string]any{
				"api_name": "getUserInfo",
				"data":     map[string]any{},
			}
			infoResult, err := a.invokeWXApp(r.Context(), acc, body.AppID, effectiveProxy, getInfoPayload, func(ctx context.Context, acc *store.WechatAccount, appID string, proxy string, p map[string]any) (map[string]any, error) {
				return a.pool.OperateWXData(ctx, acc.LoginBuffer, appID, getInfoPayload, acc.ID, proxy)
			})
			if err == nil && infoResult != nil {
				if ed, ok := infoResult["encryptedData"]; ok {
					resp["encryptedData"] = ed
				}
				if iv, ok := infoResult["iv"]; ok {
					resp["iv"] = iv
				}
				tryPut(&resp, infoResult, "nickname", "nickName", "nick_name")
				tryPut(&resp, infoResult, "avatar", "avatarUrl", "head_img_url")
			}
		}
	}
	writeJSON(w, http.StatusOK, resp)
}

// handleWxEncryptKey 返回加密密钥
// POST /wx/encryptkey  body: { ref, app_id }
func (a *App) handleWxEncryptKey(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/wx/encryptkey" {
		writeError(w, http.StatusNotFound, "not found")
		return
	}
	if r.Method != http.MethodPost {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	var body struct {
		Ref   string `json:"ref"`
		AppID string `json:"app_id"`
	}
	if err := decodeOptionalJSON(r, &body); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON: "+err.Error())
		return
	}
	if body.Ref == "" {
		writeError(w, http.StatusBadRequest, "ref is required")
		return
	}
	acc, ok := a.resolveAccountRef(w, r, body.Ref)
	if !ok {
		return
	}
	sessionKey := ""
	if acc.Credentials != nil {
		sessionKey = stringFromAny(acc.Credentials["session_key"])
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"openid":      acc.OpenID,
		"session_key": sessionKey,
		"key":         sessionKey,
	})
}

// handleWxOAuth 处理微信 OAuth 回调 - 通过 iLink 真实执行 code2Session
// POST /wx/oauth  body: { code, state, ref, appid }
func (a *App) handleWxOAuth(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/wx/oauth" {
		writeError(w, http.StatusNotFound, "not found")
		return
	}
	if r.Method != http.MethodPost {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	var body struct {
		Code   string `json:"code"`
		State  string `json:"state"`
		Ref    string `json:"ref"`
		AppID  string `json:"appid"`
		OpenID string `json:"openid"`
	}
	if err := decodeOptionalJSON(r, &body); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON: "+err.Error())
		return
	}
	if body.Code == "" {
		writeError(w, http.StatusBadRequest, "code is required")
		return
	}
	if body.Ref == "" {
		body.Ref = body.OpenID
	}
	if body.AppID == "" {
		body.AppID = body.Ref
	}
	if body.State == "" {
		body.State = fmt.Sprintf("oauth_%d", time.Now().UnixNano())
	}
	acc, ok := a.resolveAccountRef(w, r, body.Ref)
	if !ok {
		return
	}
	effectiveProxy := resolveEffectiveProxy(acc.BoundProxy, a.cfg.TCPProxy)
	var appID string
	if body.AppID != "" && body.AppID != acc.OpenID {
		appID = body.AppID
	}
	if appID != "" && acc.LoginBuffer != "" {
		oauthPayload := map[string]any{
			"api_name": "code2Session",
			"data":     map[string]any{"code": body.Code},
		}
		result, err := a.invokeWXApp(r.Context(), acc, appID, effectiveProxy, oauthPayload, func(ctx context.Context, acc *store.WechatAccount, appID string, proxy string, p map[string]any) (map[string]any, error) {
			return a.pool.OperateWXData(ctx, acc.LoginBuffer, appID, oauthPayload, acc.ID, proxy)
		})
		if err != nil {
			var expired accountExpiredError
			if errors.As(err, &expired) {
				writeError(w, http.StatusConflict, "account login_buffer expired; re-scan required")
			} else {
				writeError(w, http.StatusBadGateway, "oauth failed: "+err.Error())
			}
			return
		}
		a.db.InsertAuditLog(r.Context(), "oauth", acc.OpenID, appID, r.RemoteAddr)
		writeJSON(w, http.StatusOK, map[string]any{
			"openid": acc.OpenID,
			"appid":  appID,
			"state":  body.State,
			"status": "ok",
			"result": result,
		})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"openid": acc.OpenID,
		"code":   body.Code,
		"state":  body.State,
		"status": "received",
	})
}

// handleWxAutoOAuth 自动 OAuth 登录
// POST /wx/autoauth  body: { code, app_id, ref }
func (a *App) handleWxAutoOAuth(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/wx/autoauth" {
		writeError(w, http.StatusNotFound, "not found")
		return
	}
	if r.Method != http.MethodPost {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	var body struct {
		Code  string `json:"code"`
		AppID string `json:"app_id"`
		Ref   string `json:"ref"`
	}
	if err := decodeOptionalJSON(r, &body); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON: "+err.Error())
		return
	}
	if body.Code == "" || body.AppID == "" {
		writeError(w, http.StatusBadRequest, "code and app_id are required")
		return
	}
	acc, ok := a.resolveAccountRef(w, r, body.Ref)
	if !ok {
		return
	}
	effectiveProxy := resolveEffectiveProxy(acc.BoundProxy, a.cfg.TCPProxy)
	result, err := a.invokeWXApp(r.Context(), acc, body.AppID, effectiveProxy, nil, func(ctx context.Context, acc *store.WechatAccount, appID string, proxy string, _ map[string]any) (map[string]any, error) {
		return a.pool.GetCode(ctx, acc.LoginBuffer, appID, acc.ID, proxy)
	})
	if err != nil {
		var expired accountExpiredError
		if errors.As(err, &expired) {
			writeError(w, http.StatusConflict, "account login_buffer expired; re-scan required")
		} else {
			writeError(w, http.StatusBadGateway, "auto oauth failed: "+err.Error())
		}
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"openid": acc.OpenID,
		"code":   result["code"],
	})
}

// handleWxHeart 心跳保活 - 检查账号活跃度并返回状态
// POST /wx/heart  body: { ref }
func (a *App) handleWxHeart(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/wx/heart" {
		writeError(w, http.StatusNotFound, "not found")
		return
	}
	if r.Method != http.MethodPost {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	var body struct {
		Ref    string `json:"ref"`
		OpenID string `json:"openid"`
	}
	_ = decodeOptionalJSON(r, &body)
	if body.Ref == "" {
		body.Ref = body.OpenID
	}
	status := "ok"
	lastChecked := int64(0)
	if body.Ref != "" {
		acc, ok := a.resolveAccountRef(w, r, body.Ref)
		if !ok {
			return
		}
		status = a.refreshLiveness(r.Context(), acc)
		acc, err := a.db.GetAccount(r.Context(), acc.ID)
		if err == nil && acc.LastCheckedAt != nil {
			lastChecked = *acc.LastCheckedAt
		}
	}
	a.db.InsertAuditLog(r.Context(), "heart", body.Ref, "", r.RemoteAddr)
	writeJSON(w, http.StatusOK, map[string]any{
		"status":       status,
		"timestamp":    time.Now().Unix(),
		"last_checked": lastChecked,
	})
}

// handleWxQRCodeAuth 处理二维码认证回调 - 真实扫描 scene，匹配本地账号
// POST /wx/qrcodeauth  body: { data, ref } 或兼容格式 { openid, uuid, scene }
func (a *App) handleWxQRCodeAuth(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/wx/qrcodeauth" {
		writeError(w, http.StatusNotFound, "not found")
		return
	}
	if r.Method != http.MethodPost {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	var body struct {
		Data   string `json:"data"`
		Ref    string `json:"ref"`
		OpenID string `json:"openid"`
		UUID   string `json:"uuid"`
		Scene  string `json:"scene"`
	}
	if err := decodeOptionalJSON(r, &body); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON: "+err.Error())
		return
	}
	if body.Ref == "" {
		body.Ref = body.OpenID
	}
	if body.Ref == "" {
		writeError(w, http.StatusBadRequest, "ref or openid is required")
		return
	}
	if body.Data == "" {
		body.Data = body.UUID
		if body.Data == "" {
			body.Data = body.Scene
		}
	}
	ctx := r.Context()
	if body.OpenID != "" {
		accounts, err := a.db.ListAccounts(ctx)
		if err == nil {
			for _, acc := range accounts {
				if acc.OpenID == body.OpenID {
					a.db.InsertAuditLog(ctx, "qrcodeauth_match", acc.OpenID, body.Data, r.RemoteAddr)
					writeJSON(w, http.StatusOK, map[string]any{
						"status":   "found",
						"scene":    body.Data,
						"openid":   acc.OpenID,
						"nickname": acc.Nickname,
						"avatar":   acc.Avatar,
					})
					return
				}
			}
		}
		a.db.InsertAuditLog(ctx, "qrcodeauth_notfound", body.OpenID, body.Data, r.RemoteAddr)
		writeJSON(w, http.StatusOK, map[string]any{"status": "not_found", "scene": body.Data, "openid": body.OpenID})
		return
	}
	acc, ok := a.resolveAccountRef(w, r, body.Ref)
	if !ok {
		return
	}
	a.db.InsertAuditLog(ctx, "qrcodeauth", acc.OpenID, body.Data, r.RemoteAddr)
	writeJSON(w, http.StatusOK, map[string]any{
		"openid":   acc.OpenID,
		"nickname": acc.Nickname,
		"uuid":     body.Data,
		"status":   "received",
	})
}

// handleWxCallInit 小程序云函数初始化（触发静默注册，确保session存在）
// POST /wx/call/init  body: { appid, openid }
func (a *App) handleWxCallInit(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	var body struct {
		AppID  string `json:"appid"`
		OpenID string `json:"openid"`
	}
	if err := decodeOptionalJSON(r, &body); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON: "+err.Error())
		return
	}
	if body.OpenID == "" {
		writeError(w, http.StatusBadRequest, "openid is required")
		return
	}
	if body.AppID == "" {
		writeError(w, http.StatusBadRequest, "appid is required")
		return
	}
	acc, ok := a.resolveAccountRef(w, r, body.OpenID)
	if !ok {
		return
	}
	effectiveProxy := resolveEffectiveProxy(acc.BoundProxy, a.cfg.TCPProxy)
	result, err := a.invokeWXApp(r.Context(), acc, body.AppID, effectiveProxy, nil, func(ctx context.Context, acc *store.WechatAccount, appID string, proxy string, _ map[string]any) (map[string]any, error) {
		return a.pool.GetCode(ctx, acc.LoginBuffer, appID, acc.ID, proxy)
	})
	if err != nil {
		var expired accountExpiredError
		if errors.As(err, &expired) {
			writeError(w, http.StatusConflict, "account login_buffer expired; re-scan required")
		} else {
			writeError(w, http.StatusBadGateway, "init failed: "+err.Error())
		}
		return
	}
	a.db.InsertAuditLog(r.Context(), "wx_call_init", acc.OpenID, body.AppID, r.RemoteAddr)
	writeJSON(w, http.StatusOK, map[string]any{"openid": acc.OpenID, "appid": body.AppID, "status": "initialized", "result": result})
}

// handleWxCloudCall 小程序云函数调用
// POST /wx/cloud/call  body: { appid, openid, api_name, data }
func (a *App) handleWxCloudCall(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	var body struct {
		AppID   string `json:"appid"`
		OpenID  string `json:"openid"`
		APIName string `json:"api_name"`
		Data    string `json:"data"`
	}
	if err := decodeOptionalJSON(r, &body); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON: "+err.Error())
		return
	}
	if body.OpenID == "" {
		writeError(w, http.StatusBadRequest, "openid is required")
		return
	}
	if body.AppID == "" {
		writeError(w, http.StatusBadRequest, "appid is required")
		return
	}
	acc, ok := a.resolveAccountRef(w, r, body.OpenID)
	if !ok {
		return
	}
	payload := map[string]any{}
	if body.APIName != "" {
		payload["api_name"] = body.APIName
	}
	if body.Data != "" {
		var parsed any
		if err := json.Unmarshal([]byte(body.Data), &parsed); err == nil {
			payload["data"] = parsed
		} else {
			payload["data"] = body.Data
		}
	}
	effectiveProxy := resolveEffectiveProxy(acc.BoundProxy, a.cfg.TCPProxy)
	result, err := a.invokeWXApp(r.Context(), acc, body.AppID, effectiveProxy, payload, func(ctx context.Context, acc *store.WechatAccount, appID string, proxy string, p map[string]any) (map[string]any, error) {
		return a.pool.OperateWXData(ctx, acc.LoginBuffer, appID, payload, acc.ID, proxy)
	})
	if err != nil {
		var expired accountExpiredError
		if errors.As(err, &expired) {
			writeError(w, http.StatusConflict, "account login_buffer expired; re-scan required")
		} else {
			writeError(w, http.StatusBadGateway, "cloud call failed: "+err.Error())
		}
		return
	}
	a.db.InsertAuditLog(r.Context(), "wx_cloud_call", acc.OpenID, body.AppID, r.RemoteAddr)
	writeJSON(w, http.StatusOK, map[string]any{"openid": acc.OpenID, "appid": body.AppID, "result": result})
}

// handleDashboard 返回账号概览
func (a *App) handleDashboard(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	accs, err := a.db.ListAccounts(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, "list accounts: "+err.Error())
		return
	}
	total := len(accs)
	alive := 0
	expired := 0
	withProxy := 0
	items := make([]map[string]any, 0, total)
	for _, acc := range accs {
		status := "unknown"
		if acc.Status != nil {
			status = *acc.Status
		}
		if status == "alive" {
			alive++
		} else {
			expired++
		}
		if acc.BoundProxy != "" {
			withProxy++
		}
		item := map[string]any{
			"id":           acc.ID,
			"openid":       acc.OpenID,
			"status":       status,
			"disabled":     acc.Disabled,
			"bound_proxy":  acc.BoundProxy,
			"last_checked": "",
		}
		if acc.Nickname != nil {
			item["nickname"] = *acc.Nickname
		}
		if acc.Avatar != nil {
			item["avatar"] = *acc.Avatar
		}
		if acc.LastCheckedAt != nil {
			item["last_checked"] = time.Unix(*acc.LastCheckedAt, 0).Format("2006-01-02 15:04:05")
		}
		items = append(items, item)
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"summary": map[string]any{
			"total":      total,
			"alive":      alive,
			"expired":    expired,
			"with_proxy": withProxy,
		},
		"accounts": items,
	})
}

func (a *App) handleGetPhoneNumber(w http.ResponseWriter, r *http.Request) {
	if !acceptWXAppRoute(w, r, "/wxapp/getPhoneNumber") {
		return
	}
	a.callWXApp(w, r, false, a.invokeGetPhoneNumber)
}

func (a *App) handleWxGetPhoneNumber(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	var body struct {
		AppID  string `json:"appid"`
		OpenID string `json:"openid"`
	}
	if err := decodeOptionalJSON(r, &body); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON: "+err.Error())
		return
	}
	if body.OpenID == "" {
		writeError(w, http.StatusBadRequest, "openid is required")
		return
	}
	if body.AppID == "" {
		writeError(w, http.StatusBadRequest, "appid is required")
		return
	}
	acc, ok := a.resolveAccountRef(w, r, body.OpenID)
	if !ok {
		return
	}
	effectiveProxy := resolveEffectiveProxy(acc.BoundProxy, a.cfg.TCPProxy)
	result, err := a.invokeWXApp(r.Context(), acc, body.AppID, effectiveProxy, nil, a.invokeGetPhoneNumber)
	if err != nil {
		var expired accountExpiredError
		if errors.As(err, &expired) {
			writeError(w, http.StatusConflict, "account login_buffer expired; re-scan required")
		} else {
			writeError(w, http.StatusBadGateway, "get phone number failed: "+err.Error())
		}
		return
	}
	writeJSON(w, http.StatusOK, result)
}

func (a *App) handleOperateWXData(w http.ResponseWriter, r *http.Request) {
	if !acceptWXAppRoute(w, r, "/wxapp/operateWxData") {
		return
	}
	a.callWXApp(w, r, true, a.invokeOperateWXData)
}

func (a *App) handleCheckToken(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet && r.Method != http.MethodPost {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	ref := strings.TrimSpace(r.URL.Query().Get("ref"))
	if ref == "" {
		ref = strings.TrimSpace(r.URL.Query().Get("account_id"))
	}
	if ref == "" {
		writeError(w, http.StatusBadRequest, "ref or account_id query param is required")
		return
	}
	acc, ok := a.resolveAccountRef(w, r, ref)
	if !ok {
		return
	}
	status := a.refreshLiveness(r.Context(), acc)
	writeJSON(w, http.StatusOK, map[string]any{
		"openid":   acc.OpenID,
		"uin":      acc.UIN,
		"nickname": acc.Nickname,
		"status":   status,
	})
}

func acceptWXAppRoute(w http.ResponseWriter, r *http.Request, path string) bool {
	if r.URL.Path != path {
		writeError(w, http.StatusNotFound, "not found")
		return false
	}
	if r.Method != http.MethodPost {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return false
	}
	return true
}

type accountRefIn struct {
	Ref string `json:"ref"`
}

type wxappRequest struct {
	Ref     string         `json:"ref"`
	AppID   string         `json:"app_id"`
	Payload map[string]any `json:"payload"`
}

type wxappCall func(ctx context.Context, acc *store.WechatAccount, appID string, proxy string, payload map[string]any) (map[string]any, error)

func (a *App) callWXApp(w http.ResponseWriter, r *http.Request, requirePayload bool, call wxappCall) {
	var body wxappRequest
	if err := decodeOptionalJSON(r, &body); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON: "+err.Error())
		return
	}
	if body.Ref == "" {
		writeError(w, http.StatusBadRequest, "ref is required")
		return
	}
	if body.AppID == "" {
		writeError(w, http.StatusBadRequest, "app_id is required")
		return
	}
	if requirePayload && body.Payload == nil {
		writeError(w, http.StatusBadRequest, "payload is required")
		return
	}
	acc, ok := a.resolveAccountRef(w, r, body.Ref)
	if !ok {
		return
	}
	effectiveProxy := resolveEffectiveProxy(acc.BoundProxy, a.cfg.TCPProxy)
	result, err := a.invokeWXApp(r.Context(), acc, body.AppID, effectiveProxy, body.Payload, call)
	if err != nil {
		var expired accountExpiredError
		switch {
		case errors.As(err, &expired):
			writeError(w, http.StatusConflict, "account login_buffer expired (refresh failed); re-scan required")
		default:
			writeError(w, http.StatusBadGateway, "call failed: "+err.Error())
		}
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"openid": acc.OpenID, "result": result})
}

func decodeOptionalJSON(r *http.Request, dst any) error {
	err := json.NewDecoder(r.Body).Decode(dst)
	if errors.Is(err, io.EOF) {
		return nil
	}
	return err
}

func (a *App) resolveAccountFromQuery(w http.ResponseWriter, r *http.Request) (*store.WechatAccount, bool) {
	ref := strings.TrimSpace(r.URL.Query().Get("ref"))
	if ref == "" {
		writeError(w, http.StatusBadRequest, "ref query param is required")
		return nil, false
	}
	return a.resolveAccountRef(w, r, ref)
}

func (a *App) resolveAccountRef(w http.ResponseWriter, r *http.Request, ref string) (*store.WechatAccount, bool) {
	ref = strings.TrimSpace(ref)
	if ref == "" {
		writeError(w, http.StatusBadRequest, "ref is required")
		return nil, false
	}
	acc, err := a.db.ResolveAccount(r.Context(), ref)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			writeError(w, http.StatusNotFound, "account not found: "+ref)
		} else {
			writeError(w, http.StatusInternalServerError, err.Error())
		}
		return nil, false
	}
	return acc, true
}

func (a *App) refreshAll(w http.ResponseWriter, r *http.Request) {
	accounts, err := a.db.ListAccounts(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	out := make([]map[string]any, 0, len(accounts))
	for _, acc := range accounts {
		out = append(out, refreshOut(acc, a.refreshLiveness(r.Context(), acc)))
	}
	writeJSON(w, http.StatusOK, out)
}

func (a *App) resyncAll(w http.ResponseWriter, r *http.Request) {
	accounts, err := a.db.ListAccounts(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	out := make([]store.AccountPublic, 0, len(accounts))
	for _, acc := range accounts {
		updated, err := a.resyncProfile(r.Context(), acc)
		if err != nil {
			writeError(w, http.StatusInternalServerError, err.Error())
			return
		}
		out = append(out, updated.Public())
	}
	writeJSON(w, http.StatusOK, out)
}

func (a *App) serveAvatar(w http.ResponseWriter, r *http.Request, acc *store.WechatAccount) {
	if acc.Avatar != nil && *acc.Avatar != "" {
		if _, err := os.Stat(*acc.Avatar); err == nil {
			w.Header().Set("Content-Type", "image/jpeg")
			http.ServeFile(w, r, *acc.Avatar)
			return
		}
		if strings.HasPrefix(*acc.Avatar, "http://") || strings.HasPrefix(*acc.Avatar, "https://") {
			http.Redirect(w, r, *acc.Avatar, http.StatusFound)
			return
		}
	}
	writeError(w, http.StatusNotFound, "no avatar")
}

func (a *App) storeFromScan(ctx context.Context, loginBuffer string, creds protocol.LoginBufferCredentials, userInfo map[string]any) (*store.WechatAccount, error) {
	openid := creds.OpenID
	nick := pickNickname(userInfo, creds.Nickname)
	avatar := a.resolveAvatar(ctx, openid, userInfo)
	status := "alive"
	return a.db.UpsertAccount(ctx, openid, loginBuffer, stringPtrMaybe(nick), stringPtrMaybe(nick), stringPtrMaybe(avatar), userInfo, creds.ToMap(), &status)
}

func (a *App) refreshLiveness(ctx context.Context, acc *store.WechatAccount) string {
	if acc.Credentials == nil {
		_ = a.db.SetAccountStatus(ctx, acc.ID, "unknown")
		return "unknown"
	}
	creds := protocol.CredentialsFromMap(acc.Credentials)
	result, err := a.qr.RefreshLoginBuffer(ctx, creds)
	if err != nil {
		_ = a.db.SetAccountStatus(ctx, acc.ID, "expired")
		return "expired"
	}
	_ = a.db.SetAccountCredential(ctx, acc.ID, result.LoginBuffer, result.Credentials.ToMap())
	_ = a.db.SetAccountStatus(ctx, acc.ID, "alive")
	if avatar := a.resolveAvatar(ctx, acc.OpenID, acc.UserInfo); avatar != "" {
		_ = a.db.SetAccountProfile(ctx, acc.ID, acc.Nickname, &avatar, acc.UserInfo)
	}
	return "alive"
}

func (a *App) resyncProfile(ctx context.Context, acc *store.WechatAccount) (*store.WechatAccount, error) {
	nick := pickNickname(acc.UserInfo, deref(acc.Nickname))
	avatar := a.resolveAvatar(ctx, acc.OpenID, acc.UserInfo)
	if avatar == "" {
		avatar = deref(acc.Avatar)
	}
	if err := a.db.SetAccountProfile(ctx, acc.ID, stringPtrMaybe(nick), stringPtrMaybe(avatar), acc.UserInfo); err != nil {
		return nil, err
	}
	return a.db.GetAccount(ctx, acc.ID)
}

type accountExpiredError struct{ openid string }

func (e accountExpiredError) Error() string { return "account expired: " + e.openid }

func (a *App) invokeWXApp(ctx context.Context, acc *store.WechatAccount, appID string, proxy string, payload map[string]any, call wxappCall) (map[string]any, error) {
	if _, err := a.db.GetSession(ctx, acc.ID, proxy); err == nil {
		result, err := call(ctx, acc, appID, proxy, payload)
		if err == nil {
			return result, nil
		}
		_ = a.db.InvalidateSession(ctx, acc.ID, proxy)
	}
	status := a.refreshLiveness(ctx, acc)
	if status != "alive" {
		return nil, accountExpiredError{openid: acc.OpenID}
	}
	fresh, err := a.db.GetAccount(ctx, acc.ID)
	if err == nil && fresh != nil {
		acc = fresh
	}
	return call(ctx, acc, appID, proxy, payload)
}

func (a *App) invokeGetCode(ctx context.Context, acc *store.WechatAccount, appID string, proxy string, _ map[string]any) (map[string]any, error) {
	return a.pool.GetCode(ctx, acc.LoginBuffer, appID, acc.ID, proxy)
}

func (a *App) invokeGetPhoneNumber(ctx context.Context, acc *store.WechatAccount, appID string, proxy string, _ map[string]any) (map[string]any, error) {
	return a.pool.GetPhoneNumber(ctx, acc.LoginBuffer, appID, acc.ID, proxy)
}

func (a *App) invokeOperateWXData(ctx context.Context, acc *store.WechatAccount, appID string, proxy string, payload map[string]any) (map[string]any, error) {
	return a.pool.OperateWXData(ctx, acc.LoginBuffer, appID, payload, acc.ID, proxy)
}

func refreshOut(acc *store.WechatAccount, status string) map[string]any {
	return map[string]any{"id": acc.ID, "openid": acc.OpenID, "uin": acc.UIN, "nickname": acc.Nickname, "status": status}
}

func pickNickname(userInfo map[string]any, fallback string) string {
	if s := stringFromAny(userInfo["nick_name"]); s != "" {
		return s
	}
	return fallback
}

func pickAvatarURL(userInfo map[string]any) string {
	for _, k := range []string{"head_img_url", "head_url", "headimgurl", "avatar"} {
		if s := stringFromAny(userInfo[k]); s != "" {
			return s
		}
	}
	return ""
}

func (a *App) resolveAvatar(ctx context.Context, openid string, userInfo map[string]any) string {
	u := pickAvatarURL(userInfo)
	if u == "" {
		return ""
	}
	dest := a.resources.avatarPath(openid)
	if downloadAvatar(ctx, u, dest, a.cfg.AvatarTimeout) {
		return dest
	}
	return u
}

func downloadAvatar(ctx context.Context, url, dest string, timeout time.Duration) bool {
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return false
	}
	client := &http.Client{Timeout: timeout}
	resp, err := client.Do(req)
	if err != nil {
		return false
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(io.LimitReader(resp.Body, 8<<20))
	if err != nil || resp.StatusCode != 200 || !looksLikeImage(data) {
		return false
	}
	_ = os.MkdirAll(filepath.Dir(dest), 0o755)
	return os.WriteFile(dest, data, 0o644) == nil
}

func looksLikeImage(data []byte) bool {
	if len(data) < 64 {
		return false
	}
	magics := [][]byte{{0xff, 0xd8, 0xff}, {0x89, 'P', 'N', 'G', '\r', '\n', 0x1a, '\n'}, []byte("GIF87a"), []byte("GIF89a")}
	for _, m := range magics {
		if strings.HasPrefix(string(data), string(m)) {
			return true
		}
	}
	return false
}

func (a *App) getQRSession(id string) *qr.Session {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.qrSessions[id]
}

func (a *App) dropQRSession(id string) {
	a.mu.Lock()
	delete(a.qrSessions, id)
	a.mu.Unlock()
	_ = os.Remove(a.resources.qrPath(id))
}

func (a *App) pruneQR() {
	a.mu.Lock()
	var drop []string
	for sid, sess := range a.qrSessions {
		if sess.Age() > a.cfg.QRSessionTTL {
			drop = append(drop, sid)
		}
	}
	for _, sid := range drop {
		delete(a.qrSessions, sid)
	}
	a.mu.Unlock()
	for _, sid := range drop {
		_ = os.Remove(a.resources.qrPath(sid))
	}
}

func (a *App) cleanupQR(keep map[string]bool) {
	files, _ := filepath.Glob(filepath.Join(a.resources.QR, "*.jpg"))
	for _, f := range files {
		sid := strings.TrimSuffix(filepath.Base(f), ".jpg")
		if !keep[sid] {
			_ = os.Remove(f)
		}
	}
}

func terminalQR(status string) bool {
	return status == "expired" || status == "cancelled" || status == "unknown"
}

type apiEnvelope struct {
	Status bool   `json:"status"`
	Code   int    `json:"code"`
	Msg    string `json:"msg"`
	Data   any    `json:"data"`
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	writeRawJSON(w, status, apiEnvelope{
		Status: true,
		Code:   0,
		Msg:    "success",
		Data:   v,
	})
}

func writeRawJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	enc := json.NewEncoder(w)
	enc.SetEscapeHTML(false)
	_ = enc.Encode(v)
}

func writeError(w http.ResponseWriter, status int, detail string) {
	writeRawJSON(w, status, apiEnvelope{
		Status: false,
		Code:   status,
		Msg:    detail,
		Data:   nil,
	})
}

func requestLogger(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		next.ServeHTTP(w, r)
	})
}

func serveFileOrText(w http.ResponseWriter, r *http.Request, path, fallback string) {
	if _, err := os.Stat(path); err == nil {
		http.ServeFile(w, r, path)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_, _ = w.Write([]byte(fallback))
}

func stringFromAny(v any) string {
	if s, ok := v.(string); ok {
		return s
	}
	return ""
}

func stringPtrMaybe(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}

func deref(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}

func safeName(s string) string {
	var b strings.Builder
	for _, r := range s {
		if (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') || r == '-' || r == '_' {
			b.WriteRune(r)
		}
	}
	return b.String()
}

func resolveEffectiveProxy(bound, global string) string {
	if bound != "" {
		return bound
	}
	return global
}

func sortedKeys[M ~map[string]V, V any](m M) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

func tryPut(dst *map[string]any, src map[string]any, keys ...string) {
	if src == nil {
		return
	}
	for _, k := range keys {
		if v, ok := src[k]; ok && v != nil {
			s, isStr := v.(string)
			if isStr && s != "" {
				(*dst)[keys[0]] = s
				return
			}
		}
	}
}

// handleWxAppMsgExt 处理小程序消息扩展
// POST /wx/appmsgext  body: { ref, app_id, payload }
func (a *App) handleWxAppMsgExt(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/wx/appmsgext" {
		writeError(w, http.StatusNotFound, "not found")
		return
	}
	if r.Method != http.MethodPost {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	var body struct {
		Ref     string         `json:"ref"`
		AppID   string         `json:"app_id"`
		Payload map[string]any `json:"payload"`
	}
	if err := decodeOptionalJSON(r, &body); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON: "+err.Error())
		return
	}
	if body.Ref == "" || body.AppID == "" {
		writeError(w, http.StatusBadRequest, "ref and app_id are required")
		return
	}
	acc, ok := a.resolveAccountRef(w, r, body.Ref)
	if !ok {
		return
	}
	effectiveProxy := resolveEffectiveProxy(acc.BoundProxy, a.cfg.TCPProxy)
	result, err := a.invokeWXApp(r.Context(), acc, body.AppID, effectiveProxy, body.Payload, a.invokeOperateWXData)
	if err != nil {
		var expired accountExpiredError
		if errors.As(err, &expired) {
			writeError(w, http.StatusConflict, "account login_buffer expired; re-scan required")
		} else {
			writeError(w, http.StatusBadGateway, "appmsgext failed: "+err.Error())
		}
		return
	}
	a.db.InsertAuditLog(r.Context(), "appmsgext", acc.OpenID, body.AppID, r.RemoteAddr)
	writeJSON(w, http.StatusOK, map[string]any{"openid": acc.OpenID, "app_id": body.AppID, "result": result})
}

// handleWxAppMsgLike 处理小程序消息点赞
// POST /wx/appmsglike  body: { ref, app_id, payload }
func (a *App) handleWxAppMsgLike(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/wx/appmsglike" {
		writeError(w, http.StatusNotFound, "not found")
		return
	}
	if r.Method != http.MethodPost {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	var body struct {
		Ref     string         `json:"ref"`
		AppID   string         `json:"app_id"`
		Payload map[string]any `json:"payload"`
	}
	if err := decodeOptionalJSON(r, &body); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON: "+err.Error())
		return
	}
	if body.Ref == "" || body.AppID == "" {
		writeError(w, http.StatusBadRequest, "ref and app_id are required")
		return
	}
	acc, ok := a.resolveAccountRef(w, r, body.Ref)
	if !ok {
		return
	}
	effectiveProxy := resolveEffectiveProxy(acc.BoundProxy, a.cfg.TCPProxy)
	result, err := a.invokeWXApp(r.Context(), acc, body.AppID, effectiveProxy, body.Payload, a.invokeOperateWXData)
	if err != nil {
		var expired accountExpiredError
		if errors.As(err, &expired) {
			writeError(w, http.StatusConflict, "account login_buffer expired; re-scan required")
		} else {
			writeError(w, http.StatusBadGateway, "appmsglike failed: "+err.Error())
		}
		return
	}
	a.db.InsertAuditLog(r.Context(), "appmsglike", acc.OpenID, body.AppID, r.RemoteAddr)
	writeJSON(w, http.StatusOK, map[string]any{"openid": acc.OpenID, "app_id": body.AppID, "result": result})
}

// handleProxiesDispatcher 代理池管理路由分发
func (a *App) handleProxiesDispatcher(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		a.handleListProxies(w, r)
	case http.MethodPost:
		a.handleAddProxy(w, r)
	case http.MethodPatch:
		a.handleUpdateProxy(w, r)
	case http.MethodDelete:
		a.handleDeleteProxy(w, r)
	default:
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
	}
}

// handleListProxies GET /api/proxies
func (a *App) handleListProxies(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	proxies, err := a.db.ListProxies(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, "list proxies: "+err.Error())
		return
	}
	if proxies == nil {
		proxies = []*store.Proxy{}
	}
	writeJSON(w, http.StatusOK, proxies)
}

// handleAddProxy POST /api/proxies  body: { url, name }
func (a *App) handleAddProxy(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	var body struct {
		URL  string `json:"url"`
		Name string `json:"name"`
	}
	if err := decodeOptionalJSON(r, &body); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON: "+err.Error())
		return
	}
	if body.URL == "" {
		writeError(w, http.StatusBadRequest, "url is required")
		return
	}
	proxy, err := a.db.AddProxy(r.Context(), body.URL, body.Name)
	if err != nil {
		writeError(w, http.StatusConflict, "add proxy: "+err.Error())
		return
	}
	a.db.InsertAuditLog(r.Context(), "add_proxy", body.URL, "", r.RemoteAddr)
	writeJSON(w, http.StatusOK, proxy)
}

// handleUpdateProxy PATCH /api/proxies  body: { id, url, name, enabled }
func (a *App) handleUpdateProxy(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPatch && r.Method != http.MethodPost {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	var body struct {
		ID      int64  `json:"id"`
		URL     string `json:"url"`
		Name    string `json:"name"`
		Enabled *bool  `json:"enabled"`
	}
	if err := decodeOptionalJSON(r, &body); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON: "+err.Error())
		return
	}
	if body.ID == 0 {
		writeError(w, http.StatusBadRequest, "id is required")
		return
	}
	enabled := true
	if body.Enabled != nil {
		enabled = *body.Enabled
	}
	if err := a.db.UpdateProxy(r.Context(), body.ID, body.URL, body.Name, enabled); err != nil {
		writeError(w, http.StatusBadRequest, "update proxy: "+err.Error())
		return
	}
	a.db.InsertAuditLog(r.Context(), "update_proxy", fmt.Sprintf("id=%d", body.ID), "", r.RemoteAddr)
	proxy, _ := a.db.GetProxy(r.Context(), body.ID)
	writeJSON(w, http.StatusOK, proxy)
}

// handleDeleteProxy DELETE /api/proxies?id=1
func (a *App) handleDeleteProxy(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodDelete {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	idStr := r.URL.Query().Get("id")
	id, err := strconv.ParseInt(idStr, 10, 64)
	if err != nil || id <= 0 {
		writeError(w, http.StatusBadRequest, "valid id query param is required")
		return
	}
	if err := a.db.DeleteProxy(r.Context(), id); err != nil {
		writeError(w, http.StatusInternalServerError, "delete proxy: "+err.Error())
		return
	}
	a.db.InsertAuditLog(r.Context(), "delete_proxy", fmt.Sprintf("id=%d", id), "", r.RemoteAddr)
	writeJSON(w, http.StatusOK, map[string]any{"deleted": id})
}

// handleListAuditLogs GET /api/audit-logs?limit=100
func (a *App) handleListAuditLogs(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	limit := 100
	if n, err := strconv.Atoi(r.URL.Query().Get("limit")); err == nil && n > 0 {
		limit = n
	}
	logs, err := a.db.ListAuditLogs(r.Context(), limit)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "list audit logs: "+err.Error())
		return
	}
	if logs == nil {
		logs = []*store.AuditLog{}
	}
	writeJSON(w, http.StatusOK, logs)
}

// handleAccountBatch 批量操作账号
// POST /accounts/batch  body: { ids: [...], action: "refresh"|"resync"|"disable"|"enable" }
func (a *App) handleAccountBatch(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	var body struct {
		IDs    []int64 `json:"ids"`
		Action string  `json:"action"`
	}
	if err := decodeOptionalJSON(r, &body); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON: "+err.Error())
		return
	}
	if len(body.IDs) == 0 || body.Action == "" {
		writeError(w, http.StatusBadRequest, "ids and action are required")
		return
	}
	results := make([]map[string]any, 0, len(body.IDs))
	for _, id := range body.IDs {
		acc, err := a.db.GetAccount(r.Context(), id)
		if err != nil {
			results = append(results, map[string]any{"id": id, "status": "error", "error": err.Error()})
			continue
		}
		switch body.Action {
		case "refresh":
			status := a.refreshLiveness(r.Context(), acc)
			results = append(results, map[string]any{"id": id, "openid": acc.OpenID, "status": status})
		case "resync":
			updated, err := a.resyncProfile(r.Context(), acc)
			if err != nil {
				results = append(results, map[string]any{"id": id, "openid": acc.OpenID, "status": "error", "error": err.Error()})
			} else {
				results = append(results, map[string]any{"id": id, "openid": updated.OpenID, "status": "synced"})
			}
		case "disable":
			_ = a.db.SetAccountDisabled(r.Context(), id, true)
			results = append(results, map[string]any{"id": id, "openid": acc.OpenID, "status": "disabled"})
		case "enable":
			_ = a.db.SetAccountDisabled(r.Context(), id, false)
			results = append(results, map[string]any{"id": id, "openid": acc.OpenID, "status": "enabled"})
		default:
			writeError(w, http.StatusBadRequest, "unknown action: "+body.Action)
			return
		}
	}
	a.db.InsertAuditLog(r.Context(), "batch_"+body.Action, fmt.Sprintf("ids=%v", body.IDs), "", r.RemoteAddr)
	writeJSON(w, http.StatusOK, results)
}
