// Package ssmenv makes it easy to load env variables from SSM. Just
// import it (as _) and env values like LOADFROMSSM:x will be
// replaced with the value of the SSM parameter named x.
package ssmenv

import (
	"context"
	"log"
	"os"
	"strings"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/service/ssm"
)

const SSMPrefix = "LOADFROMSSM:"

func init() {
	ctx := context.Background()
	cfg, err := config.LoadDefaultConfig(ctx)
	if err != nil {
		log.Fatalf("Unable to load SDK config: %v", err)
	}
	ssmSvc := ssm.NewFromConfig(cfg)

	for _, entry := range os.Environ() {
		split := strings.SplitN(entry, "=", 2)
		if len(split) == 2 && strings.HasPrefix(split[1], SSMPrefix) {
			envKey, envVal := split[0], split[1]
			ssmKey := envVal[len(SSMPrefix):]

			// Get parameter from SSM
			out, err := ssmSvc.GetParameter(ctx, &ssm.GetParameterInput{
				Name:           aws.String(ssmKey),
				WithDecryption: aws.Bool(true),
			})
			if err != nil {
				log.Fatalf("Error looking up %q to replace env variable %q: %s", ssmKey, envKey, err)
			}

			// Set the environment variable with the SSM parameter value
			if out.Parameter != nil && out.Parameter.Value != nil {
				os.Setenv(envKey, aws.ToString(out.Parameter.Value))
			}
		}
	}
}
