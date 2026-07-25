package httpapi

import (
	"github.com/gin-gonic/gin"
)

type authGuard struct{}

func newAuthGuard() *authGuard {
	return &authGuard{}
}

func (g *authGuard) middleware() gin.HandlerFunc {
	return func(c *gin.Context) {
		c.Next()
	}
}

func (g *authGuard) validateAPIAuth(_ string) bool {
	return true
}
