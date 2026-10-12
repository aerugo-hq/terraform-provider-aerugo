// Command terraform-provider-aerugo is Aerugo's Terraform provider: an org's teams, members, models, sources, and
// rules, managed as code through Aerugo's own API (ADR 0009 in the platform's repository).
package main

import (
	"context"
	"flag"
	"log"

	"github.com/hashicorp/terraform-plugin-framework/providerserver"

	"github.com/aerugo-hq/terraform-provider-aerugo/internal/provider"
)

// Set by GoReleaser at build time.
var (
	version = "dev"
	commit  = "none"
)

func main() {
	var debug bool
	flag.BoolVar(&debug, "debug", false, "run the provider with support for debuggers such as delve")
	flag.Parse()

	err := providerserver.Serve(context.Background(), provider.New(version), providerserver.ServeOpts{
		Address: "registry.terraform.io/aerugo-hq/aerugo",
		Debug:   debug,
	})
	if err != nil {
		log.Fatalf("terraform-provider-aerugo %s (%s): %s", version, commit, err)
	}
}
