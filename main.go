package main

import (
	"fmt"
	"os"

	"github.com/openshift/osdctl/cmd"
	docgen "github.com/openshift/osdctl/hack"
	"github.com/openshift/osdctl/pkg/osdctlConfig"

	"k8s.io/cli-runtime/pkg/genericclioptions"
)

func main() {

	err := osdctlConfig.EnsureConfigFile()
	if err != nil {
		fmt.Println(err)
		return
	}

	if len(os.Args) > 1 && os.Args[1] == "docgen" {
		docgen.Main()
		return
	}

	command := cmd.NewCmdRoot(genericclioptions.IOStreams{In: os.Stdin, Out: os.Stdout, ErrOut: os.Stderr})

	if err := command.Execute(); err != nil {
		_, err := fmt.Fprintf(os.Stderr, "%v\n", err)
		if err != nil {
			fmt.Println("Error while printing to stderr: ", err.Error())
		}
		os.Exit(1)
	}
}
