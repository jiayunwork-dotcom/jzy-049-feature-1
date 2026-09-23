// Command server 启动稳态高斯烟羽扩散后端服务（Gin + PostgreSQL 16）。
package main

import (
	"context"
	"log"
	"net/http"
	"time"

	"github.com/gin-gonic/gin"

	"gaussian-plume/internal/api"
	"gaussian-plume/internal/config"
	"gaussian-plume/internal/job"
	"gaussian-plume/internal/multijob"
	"gaussian-plume/internal/store"
)

func main() {
	cfg := config.Load()

	// 数据库可能比服务晚就绪（compose 启动顺序），带退避重试。
	pg, err := connectWithRetry(cfg.DatabaseURL, 30, 2*time.Second)
	if err != nil {
		log.Fatalf("数据库不可用: %v", err)
	}
	defer pg.Close()

	jobs := job.NewService(pg)
	multiJobs := multijob.NewService(pg) // 同一 PostgreSQL 支撑多源叠加作业

	if cfg.SeedDemoOnBoot {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		if err := jobs.SeedDemo(ctx); err != nil {
			log.Printf("警告: 预置示范作业失败: %v", err)
		} else {
			log.Printf("示范作业已就绪: job_id=%s", job.DemoJobID)
		}
		cancel()
	}

	gin.SetMode(gin.ReleaseMode)
	r := gin.New()
	r.Use(gin.Logger(), gin.Recovery())

	srv := api.NewServer(jobs).WithMulti(multiJobs)
	srv.Register(r)

	log.Printf("高斯烟羽扩散服务监听 %s", cfg.HTTPAddr)
	httpServer := &http.Server{
		Addr:              cfg.HTTPAddr,
		Handler:           r,
		ReadHeaderTimeout: 10 * time.Second,
	}
	if err := httpServer.ListenAndServe(); err != nil && err != http.ErrServerClosed {
		log.Fatalf("HTTP 服务退出: %v", err)
	}
}

// connectWithRetry 在数据库暂不可用时按固定间隔重试。
func connectWithRetry(dsn string, attempts int, interval time.Duration) (*store.Postgres, error) {
	var lastErr error
	for i := 0; i < attempts; i++ {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		pg, err := store.New(ctx, dsn)
		cancel()
		if err == nil {
			return pg, nil
		}
		lastErr = err
		log.Printf("等待数据库就绪（第 %d/%d 次）: %v", i+1, attempts, err)
		time.Sleep(interval)
	}
	return nil, lastErr
}
