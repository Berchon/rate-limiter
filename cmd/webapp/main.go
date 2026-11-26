// Package main is the entry point for the rate-limiter HTTP service.
package main

import "github.com/Berchon/rate-limiter/internal/infra/app"

func main() {
	app := app.NewApp()
	app.Start()
}
