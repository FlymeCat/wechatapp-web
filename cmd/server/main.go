package main

import (
	"log"

	"wechatapp-web/internal/config"
	"wechatapp-web/internal/router"

	_ "wechatapp-web/docs" // Swagger generated docs
)

// @title           wechatapp-web API
// @version         1.0.0
// @description     Image background replacement API. Uploads an image, removes its
// @description     background via the remove.bg service, then optionally composites
// @description     the cutout onto a new background (solid color or another image).
// @termsOfService  http://swagger.io/terms/

// @contact.name   wechatapp-web
// @contact.url    http://www.swagger.io/support
// @contact.email  support@swagger.io

// @license.name  Apache 2.0
// @license.url   http://www.apache.org/licenses/LICENSE-2.0.html

// @host      localhost:8080
// @BasePath  /api/v1

// @schemes   http

// @securityDefinitions.apikey BearerAuth
// @in                         header
// @name                       Authorization
// @description                JWT issued by POST /auth/login, sent as "Bearer <token>"
func main() {
	cfg := config.Load()

	if cfg.RemoveBGAPIKey == "" {
		log.Println("[warn] REMOVE_BG_API_KEY is not set; the replace-background endpoint will fail until it is configured")
	}

	r := router.New(cfg)

	log.Printf("listening on %s", cfg.ListenAddr)
	if err := r.Run(cfg.ListenAddr); err != nil {
		log.Fatalf("server exited: %v", err)
	}
}
