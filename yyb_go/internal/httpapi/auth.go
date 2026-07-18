package httpapi

import (
	"net/http"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/gin-gonic/gin"
)

type authGuard struct {
	username string
	password string
	enabled  bool

	mu       sync.Mutex
	failures map[string]int // IP -> consecutive failures
}

func newAuthGuard() *authGuard {
	g := &authGuard{
		username: os.Getenv("YYB_USERNAME"),
		password: os.Getenv("YYB_PASSWORD"),
		failures: map[string]int{},
	}
	g.enabled = g.username != "" && g.password != ""
	return g
}

// middleware 返回 Gin 中间件。未配置用户名密码时跳过鉴权。
func (g *authGuard) middleware() gin.HandlerFunc {
	return func(c *gin.Context) {
		if !g.enabled {
			c.Next()
			return
		}
		path := c.Request.URL.Path
		// 放行所有 API 路径（/wx/、/wxapp/）和公开端点
		if isAPIPath(path) {
			c.Next()
			return
		}

		ip := c.ClientIP()
		if g.isBlocked(ip) {
			c.AbortWithStatus(http.StatusTooManyRequests)
			return
		}

		user, pass, ok := c.Request.BasicAuth()
		if !ok || user != g.username || pass != g.password {
			g.recordFailure(ip)
			c.Header("WWW-Authenticate", `Basic realm="YYB Go"`)
			c.AbortWithStatus(http.StatusUnauthorized)
			return
		}
		g.resetFailures(ip)
		c.Next()
	}
}

func isAPIPath(path string) bool {
	return strings.HasPrefix(path, "/wx/") ||
		strings.HasPrefix(path, "/wxapp/") ||
		path == "/api/dashboard" ||
		path == "/health" ||
		path == "/openapi.json"
}

func (g *authGuard) isBlocked(ip string) bool {
	g.mu.Lock()
	defer g.mu.Unlock()
	return g.failures[ip] >= 5
}

func (g *authGuard) recordFailure(ip string) {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.failures[ip]++
	// auto-unblock after 60s
	time.AfterFunc(60*time.Second, func() {
		g.mu.Lock()
		delete(g.failures, ip)
		g.mu.Unlock()
	})
}

func (g *authGuard) resetFailures(ip string) {
	g.mu.Lock()
	defer g.mu.Unlock()
	delete(g.failures, ip)
}
