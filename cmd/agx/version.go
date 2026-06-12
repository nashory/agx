package main

var (
	version = "dev"
	commit  = "unknown"
	date    = "unknown"
)

func versionString() string {
	return version + " (" + commit + ", " + date + ")"
}
