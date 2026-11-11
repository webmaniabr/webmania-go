package main

import (
	"context"
	"encoding/json"
	"fmt"
	sdk "github.com/webmaniabr/webmania-go"
	"os"
	"time"
)

func main() {
	c, e := sdk.NewNfeClient(sdk.CompanyCredentials{ConsumerKey: os.Getenv("WEBMANIA_CONSUMER_KEY"), ConsumerSecret: os.Getenv("WEBMANIA_CONSUMER_SECRET"), AccessToken: os.Getenv("WEBMANIA_ACCESS_TOKEN"), AccessTokenSecret: os.Getenv("WEBMANIA_ACCESS_TOKEN_SECRET")}, sdk.Config{})
	if e != nil {
		fmt.Fprintln(os.Stderr, e)
		os.Exit(1)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	r, e := c.GetSefazStatus(ctx, sdk.NfeGetSefazStatusParams{})
	if e != nil {
		fmt.Fprintln(os.Stderr, e)
		os.Exit(1)
	}
	b, _ := json.Marshal(r.Value)
	fmt.Println(string(b))
}
