// touch-server is the T0 foundation server for the touch-engine app
// (HUI-1746 碰一碰:商家线下活动/内容分发): net/http + embedded sqlite
// migrations + platform identity/upload integration.
//
// Subcommands:
//
//	(default)         run the API server
//	provision-tenant  create a tenant (bootstrap/ops)
//	provision-member  add a member to a tenant (bootstrap/ops)
package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/bianjiefilm/touch-engine/server/internal/config"
	"github.com/bianjiefilm/touch-engine/server/internal/httpapi"
	"github.com/bianjiefilm/touch-engine/server/internal/leads"
	"github.com/bianjiefilm/touch-engine/server/internal/notifytask"
	"github.com/bianjiefilm/touch-engine/server/internal/provision"
)

func main() {
	log.SetFlags(log.LstdFlags | log.LUTC)
	args := os.Args[1:]
	switch {
	case len(args) > 0 && args[0] == "provision-tenant":
		cmdProvisionTenant(args[1:])
	case len(args) > 0 && args[0] == "provision-member":
		cmdProvisionMember(args[1:])
	default:
		cmdServe()
	}
}

func cmdServe() {
	cfg := config.FromEnv()
	srv, err := httpapi.Open(cfg, log.Default())
	if err != nil {
		log.Fatalf("touch-server: open: %v", err)
	}
	defer srv.Close()

	// 配置门:缺依赖时打印显式清单;服务仍启动(健康探针可观测),
	// 但一切鉴权动作将 fail-closed。
	if problems := cfg.Gate(); len(problems) > 0 {
		log.Printf("touch-server: CONFIG GATE NOT SATISFIED (%d):", len(problems))
		for _, p := range problems {
			log.Printf("  - %s", p)
		}
	}
	log.Printf("touch-server: starting %s identity=platform", cfg.Describe())

	h := srv.Handler()
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	// HUI-1747: lead forwarder drives the leads outbox to platform-notify.
	// Started only with FEATURE_LEADS_CAPTURE=on AND a satisfied config gate
	// (fail-closed; the outbox keeps its facts until a later start).
	if cfg.FeatureLeadsCapture && len(cfg.Gate()) == 0 {
		poster := notifytask.NewDirectedClient(
			notifytask.New(notifytask.SideNotify, true, cfg.NotifyBaseURL, cfg.NotifyToken, cfg.AppID, "X-Notify-App-ID"), nil)
		fw := leads.NewForwarder(srv.St, poster, log.Default())
		go fw.Run(ctx, 15*time.Second)
		log.Printf("touch-server: leads forwarder started (interval 15s)")
	}

	if err := serveHTTP(ctx, cfg.HTTPAddr, h); err != nil {
		log.Fatalf("touch-server: %v", err)
	}
	log.Printf("touch-server: shut down cleanly")
}

func cmdProvisionTenant(args []string) {
	fs := flag.NewFlagSet("provision-tenant", flag.ExitOnError)
	dbPath := fs.String("db", "", "sqlite db path (required)")
	name := fs.String("name", "", "tenant name (required)")
	_ = fs.Parse(args)
	if *dbPath == "" || *name == "" {
		fatalUsage("provision-tenant requires -db and -name")
	}
	id, err := provision.Tenant(*dbPath, *name)
	must(err)
	fmt.Println(id)
}

func cmdProvisionMember(args []string) {
	fs := flag.NewFlagSet("provision-member", flag.ExitOnError)
	dbPath := fs.String("db", "", "sqlite db path (required)")
	tenant := fs.String("tenant", "", "tenant id (required)")
	principal := fs.String("principal", "", "platform principal ref, usr_* (required)")
	role := fs.String("role", "staff", "owner|staff")
	name := fs.String("name", "", "display name")
	disabled := fs.Bool("disabled", false, "create as disabled")
	_ = fs.Parse(args)
	if *dbPath == "" || *tenant == "" || *principal == "" {
		fatalUsage("provision-member requires -db, -tenant, -principal")
	}
	id, err := provision.Member(*dbPath, *tenant, *principal, *role, *name, !*disabled)
	must(err)
	fmt.Println(id)
}

func fatalUsage(msg string) {
	fmt.Fprintln(os.Stderr, "touch-server: "+msg)
	os.Exit(2)
}

func must(err error) {
	if err != nil {
		log.Fatalf("touch-server: %v", err)
	}
}
