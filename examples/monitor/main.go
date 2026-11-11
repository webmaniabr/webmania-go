package main

import (
	"context"
	"fmt"
	sdk "github.com/webmaniabr/webmania-go"
	"os"
	"time"
)

func main() {
	c, e := sdk.NewMonitorClient(sdk.MonitorCredentials{Token: os.Getenv("WEBMANIA_MONITOR_TOKEN")}, sdk.Config{})
	if e != nil {
		fmt.Fprintln(os.Stderr, e)
		os.Exit(1)
	}
	it, e := c.Companies(sdk.MonitorListCompaniesParams{}, sdk.PaginationOptions{MaxItems: 75, MaxPages: 3, Timeout: time.Minute})
	if e != nil {
		fmt.Fprintln(os.Stderr, e)
		os.Exit(1)
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	n := 0
	for {
		_, ok, e := it.Next(ctx)
		if e != nil {
			fmt.Fprintln(os.Stderr, e)
			os.Exit(1)
		}
		if !ok {
			break
		}
		n++
	}
	fmt.Printf("Empresas recebidas: %d; parada: %s; continuação: %+v\n", n, it.StopReason(), it.Continuation())
}
