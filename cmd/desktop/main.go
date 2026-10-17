package main

import (
	"embed"
	"fmt"
	"os"

	"github.com/afterlune/lune/internal/desktop"
	"github.com/wailsapp/wails/v3/pkg/application"
)

//go:embed all:frontend/dist
var frontend embed.FS

func main() {
	if err := run(); err != nil {
		showStartupError(err)
		os.Exit(1)
	}
}

func run() error {
	service, err := desktop.NewDesktopService()
	if err != nil {
		return fmt.Errorf("打开 luneAgent 设置数据库失败: %w", err)
	}
	app := application.New(application.Options{
		Name:        "luneAgent",
		Description: "本地 Agent 桌面端",
		Services:    []application.Service{application.NewService(service)},
		Assets:      application.AssetOptions{Handler: desktop.WallpaperAssetHandler(application.AssetFileServerFS(frontend))},
	})
	app.Window.NewWithOptions(application.WebviewWindowOptions{
		Title: "luneAgent", Width: 1440, Height: 920, MinWidth: 980, MinHeight: 640,
		BackgroundColour: application.NewRGB(248, 249, 251),
		InitialPosition:  application.WindowCentered,
		URL:              "/",
	})
	return app.Run()
}
