package main

import (
	"context"
	"flag"
	"fmt"
	"os"

	"github.com/zoneBen/longuv/internal/app"
	"github.com/zoneBen/longuv/internal/buildinfo"
	"github.com/zoneBen/longuv/internal/config"
)

func main() {
	var (
		configPath  = flag.String("config", "", "配置文件路径（默认 <dir>/config/config.yaml）")
		baseDir     = flag.String("dir", config.DefaultBaseDir(), "运行/日志/数据根目录")
		showVer     = flag.Bool("version", false, "显示版本并退出")
		checkOnly   = flag.Bool("check-config", false, "校验配置后退出")
		dumpConfig  = flag.Bool("dump-config", false, "输出默认配置 YAML 后退出")
		runSelftest = flag.Bool("selftest", false, "执行离线协议与 pty 自检后退出")
	)
	flag.Parse()

	if *showVer {
		fmt.Printf("iadu-sim %s (commit %s, built %s)\n", buildinfo.Version, buildinfo.Commit, buildinfo.Date)
		return
	}
	if *dumpConfig {
		data, err := config.GeneratedYAML(*baseDir)
		if err != nil {
			fatal(err)
		}
		if _, err := os.Stdout.Write(data); err != nil {
			fatal(err)
		}
		return
	}
	if *runSelftest {
		if err := app.RunSelftest(); err != nil {
			fmt.Fprintf(os.Stderr, "iadu-sim: 自检失败: %v\n", err)
			os.Exit(1)
		}
		return
	}

	if *configPath == "" {
		*configPath = config.DefaultConfigPath(*baseDir)
	}
	if _, err := os.Stat(*configPath); os.IsNotExist(err) {
		if err := config.WriteDefault(*configPath, *baseDir); err != nil {
			fatal(fmt.Errorf("生成默认配置: %w", err))
		}
		fmt.Printf("已生成默认配置: %s\n", *configPath)
	} else if err != nil {
		fatal(fmt.Errorf("检查配置文件: %w", err))
	}

	if *checkOnly {
		cfg, err := config.Load(*configPath, *baseDir)
		if err != nil {
			fatal(err)
		}
		fmt.Printf("配置校验通过: %s（%d 个端口，%d 个设备，%d 条绑定）\n",
			*configPath, len(cfg.Ports), len(cfg.Devices), len(cfg.Bindings))
		return
	}

	if err := app.Run(context.Background(), app.Options{
		ConfigPath: *configPath,
		BaseDir:    *baseDir,
	}); err != nil {
		fatal(err)
	}
}

func fatal(err error) {
	fmt.Fprintf(os.Stderr, "iadu-sim: %v\n", err)
	os.Exit(1)
}
