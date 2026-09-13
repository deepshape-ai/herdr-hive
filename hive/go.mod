module github.com/deepshape-ai/herdr-hive/hive

go 1.27.1

require golang.org/x/crypto v0.57.0

require golang.org/x/sys v0.48.0 // indirect

require github.com/deepshape-ai/herdr-hive/internal/update v0.0.0

replace github.com/deepshape-ai/herdr-hive/internal/update => ../internal/update
