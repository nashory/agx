//go:build !production

package main

import "os"

var assets = os.DirFS("frontend/dist")
