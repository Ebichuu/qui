// Copyright (c) 2026, s0up and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

// Generates a tiny synthetic database for the isolated ASN smoke test.
package main

import (
	"log"
	"net"
	"os"

	"github.com/maxmind/mmdbwriter"
	"github.com/maxmind/mmdbwriter/mmdbtype"
)

func main() {
	tree, err := mmdbwriter.New(mmdbwriter.Options{DatabaseType: "GeoLite2-ASN", Description: map[string]string{"en": "Synthetic ASN fixture"}, BuildEpoch: 1700000000, IncludeReservedNetworks: true})
	if err != nil {
		log.Fatal(err)
	}
	_, network, err := net.ParseCIDR("2001:db8::/32")
	if err != nil {
		log.Fatal(err)
	}
	if err := tree.Insert(network, mmdbtype.Map{"autonomous_system_number": mmdbtype.Uint32(64512), "autonomous_system_organization": mmdbtype.String("Synthetic ASN")}); err != nil {
		log.Fatal(err)
	}
	if _, err := tree.WriteTo(os.Stdout); err != nil {
		log.Fatal(err)
	}
}
