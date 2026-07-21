package main

import (
	"flag"
	"fmt"
	"os"
	"time"

	"github.com/yanet-platform/yanet2/tests/functional/framework"
)

func main() {
	imagePath := flag.String("image", "", "path to the QEMU image")
	cloudInitISO := flag.String("cloud-init", "", "path to the cloud-init ISO")
	timeout := flag.Duration("timeout", 120*time.Minute, "cloud-init timeout")
	flag.Parse()

	if *imagePath == "" || *cloudInitISO == "" {
		fmt.Fprintln(os.Stderr, "-image and -cloud-init are required")
		os.Exit(2)
	}
	if err := framework.PrepareQEMUImage(*imagePath, *cloudInitISO, *timeout); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
