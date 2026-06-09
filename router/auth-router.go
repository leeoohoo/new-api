package router

import (
	"github.com/QuantumNous/new-api/controller"
	"github.com/QuantumNous/new-api/middleware"
	"github.com/gin-contrib/gzip"
	"github.com/gin-gonic/gin"
)

func SetAuthRouter(router *gin.Engine) {
	authRouter := router.Group("/auth")
	authRouter.Use(middleware.RouteTag("auth"))
	authRouter.Use(gzip.Gzip(gzip.DefaultCompression))
	authRouter.Use(middleware.GlobalAPIRateLimit())
	{
		authRouter.GET("/login", controller.IAMLogin)
		authRouter.GET("/callback", controller.IAMCallback)
		authRouter.GET("/logout", controller.IAMLogout)
	}
}
