package main

import (
	"github.com/KanaDoodle/CampusTrace/internal/auth"
	"github.com/KanaDoodle/CampusTrace/internal/bootstrap"
	"github.com/KanaDoodle/CampusTrace/internal/transport"
	"github.com/modelcontextprotocol/go-sdk/mcp"
	"log"
	"os"
)

func main() {
	log.SetOutput(os.Stderr)
	ctx, cancel := bootstrap.Root()
	defer cancel()
	app, err := bootstrap.Open(ctx)
	if err != nil {
		log.Print("MCP startup failed")
		return
	}
	defer app.Close()
	user, err := (auth.Service{Store: app.Store, Secret: []byte(app.Config.JWT)}).Verify(os.Getenv("MCP_TOKEN"))
	if err != nil {
		log.Print("MCP_TOKEN must be a valid CampusTrace JWT")
		return
	}
	if err = transport.MCP(app.Tools, user).Run(ctx, &mcp.StdioTransport{}); err != nil {
		log.Print("MCP stopped: ", err)
	}
}
