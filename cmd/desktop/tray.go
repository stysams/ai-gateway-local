package main

import (
	"context"
	"fmt"
	"sort"
	"sync"
	"sync/atomic"
	"time"

	"github.com/wailsapp/wails/v3/pkg/application"
)

const trayRefreshTimeout = 4 * time.Second

type trayText struct {
	running, stopped, open, refresh, logging, autostart string
	start, stop, quit, routeSuffix, errorText           string
}

var trayTexts = map[string]trayText{
	"zh-CN": {running: "网关：运行中", stopped: "网关：未运行", open: "打开主窗口", refresh: "刷新", logging: "正文日志", autostart: "登录时启动", start: "启动网关", stop: "停止网关", quit: "退出桌面", routeSuffix: "路由", errorText: "托盘操作失败"},
	"en-US": {running: "Gateway: running", stopped: "Gateway: stopped", open: "Open main window", refresh: "Refresh", logging: "Body logging", autostart: "Start at login", start: "Start gateway", stop: "Stop gateway", quit: "Quit desktop", routeSuffix: "route", errorText: "Tray action failed"},
}

type trayController struct {
	app      *application.App
	window   *application.WebviewWindow
	tray     *application.SystemTray
	api      *trayAPI
	port     int
	mu       sync.Mutex
	quitting atomic.Bool
}

func newTrayController(app *application.App, window *application.WebviewWindow, apiURL string, port int) *trayController {
	c := &trayController{app: app, window: window, api: newTrayAPI(apiURL), port: port}
	c.tray = app.SystemTray.New()
	c.tray.SetTooltip("ai-gateway")
	c.tray.OnClick(func() { c.window.Show().Focus() })
	c.refresh()
	return c
}

func (c *trayController) refresh() {
	c.mu.Lock()
	defer c.mu.Unlock()
	ctx, cancel := context.WithTimeout(context.Background(), trayRefreshTimeout)
	defer cancel()

	status, statusErr := c.api.status(ctx)
	running := statusErr == nil
	providers := []trayProvider(nil)
	config := trayConfig{}
	if running {
		providers, _ = c.api.providers(ctx)
		config, _ = c.api.config(ctx)
	}
	language := config.UI.Language
	text, ok := trayTexts[language]
	if !ok {
		text = trayTexts["zh-CN"]
	}

	menu := c.app.NewMenu()
	stateLabel := text.stopped
	if running {
		stateLabel = text.running
	}
	menu.Add(stateLabel).SetEnabled(false)
	menu.Add(text.open).OnClick(func(*application.Context) { c.window.Show().Focus() })
	menu.Add(text.refresh).OnClick(func(*application.Context) { c.refresh() })
	menu.AddSeparator()

	sort.Slice(providers, func(i, j int) bool { return providers[i].Name < providers[j].Name })
	for _, client := range []string{"codex", "claude", "grok"} {
		submenu := menu.AddSubmenu(fmt.Sprintf("%s %s", client, text.routeSuffix))
		if !running || len(providers) == 0 {
			submenu.Add("-").SetEnabled(false)
			continue
		}
		for _, provider := range providers {
			provider := provider
			checked := status.Routes[client].Provider == provider.ID
			submenu.AddRadio(provider.Name+" · "+provider.DefaultModel, checked).OnClick(func(*application.Context) {
				c.perform(func(ctx context.Context) error {
					return c.api.setRoute(ctx, client, trayRoute{Provider: provider.ID, Model: provider.DefaultModel})
				}, text)
			})
		}
	}
	menu.AddSeparator()
	logging := menu.AddCheckbox(text.logging, running && status.LoggingEnabled)
	logging.SetEnabled(running).OnClick(func(*application.Context) {
		c.perform(func(ctx context.Context) error { return c.api.setLogging(ctx, !status.LoggingEnabled) }, text)
	})
	autostart := menu.AddCheckbox(text.autostart, running && status.AutostartEnabled)
	autostart.SetEnabled(running).OnClick(func(*application.Context) {
		c.perform(func(ctx context.Context) error { return c.api.setAutostart(ctx, !status.AutostartEnabled) }, text)
	})
	menu.AddSeparator()
	if running {
		menu.Add(text.stop).OnClick(func(*application.Context) {
			c.perform(func(ctx context.Context) error {
				if err := c.api.shutdown(ctx); err != nil {
					return err
				}
				return waitGatewayStopped(c.api.base, 5*time.Second)
			}, text)
		})
	} else {
		menu.Add(text.start).OnClick(func(*application.Context) {
			c.perform(func(context.Context) error { return ensureGateway(c.port) }, text)
		})
	}
	menu.Add(text.quit).OnClick(func(*application.Context) {
		c.quitting.Store(true)
		c.app.Quit()
	})
	c.tray.SetMenu(menu)
	if running {
		c.tray.SetTooltip(stateLabel)
	} else if statusErr != nil {
		c.tray.SetTooltip(stateLabel)
	}
}

func (c *trayController) perform(operation func(context.Context) error, text trayText) {
	ctx, cancel := context.WithTimeout(context.Background(), 12*time.Second)
	err := operation(ctx)
	cancel()
	if err != nil {
		c.tray.SetTooltip(text.errorText + ": " + err.Error())
		return
	}
	c.refresh()
}

func waitGatewayStopped(base string, timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if !gatewayReachable(base + "/healthz") {
			return nil
		}
		time.Sleep(100 * time.Millisecond)
	}
	return fmt.Errorf("gateway did not stop within %s", timeout)
}
