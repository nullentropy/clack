// Command clack is a Slack desktop client built on caution
//
//	go run . -native         # asks for Slack tokens the first time
//	go run . -demo -native   # offline workspace, no tokens
package main

import (
	"context"
	"flag"
	"log"
	"net/http"
	"os"
	"time"

	"github.com/nullentropy/caution"
	"github.com/nullentropy/caution/native"
	"github.com/nullentropy/clack/chat"
)

func main() {
	nativeFlag := flag.Bool("native", false, "open as a desktop window instead of serving HTTP")
	addr := flag.String("addr", ":8787", `listen address: ":8787" or "unix:/path.sock"`)
	demo := flag.Bool("demo", false, "use the offline demo workspace instead of Slack")
	chatter := flag.Duration("chatter", 8*time.Second, "demo: gap between messages from other members (0 for none)")
	dir := flag.String("config", configDir(), "directory for settings.json and the lists of conversations open in clack")
	debug := flag.Bool("debug", false, "log each live event from Slack")
	flag.Parse()

	a := &app{dir: *dir, quit: func() { os.Exit(0) }, debug: *debug}
	if *demo {
		store := chat.NewStore(&chat.Demo{Chatter: *chatter}, *dir)
		ctx, stop := context.WithCancel(context.Background())
		if err := store.Start(ctx); err != nil {
			log.Fatal(err)
		}
		a.use(store, stop)
	} else if t := a.tokens(); t.UserToken != "" || t.AppToken != "" {
		a.connect(t, false)
	}

	http.HandleFunc("/files/", a.serveFile)
	serveFonts()
	caution.ServeClient()
	if *nativeFlag || native.InBundle() {
		if err := native.Run(a.mount, native.Options{Title: "clack", W: 1180, H: 780}); err != nil {
			log.Fatal(err)
		}
		return
	}
	log.Printf("clack on http://localhost%s", *addr)
	log.Fatal(caution.Serve(*addr, a.mount))
}
