package athenalib

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"sync"
	"time"

	awsconfig "github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/feature/s3/manager"
	"github.com/aws/aws-sdk-go-v2/service/s3"
)

var discard = discardWithClose{}

type WriteRowsOption func(*writeRowsConfig)

type writeRowsConfig struct {
	overwrite bool
	keyName   string
	dryRun    bool
}

// WithOverwrite sets whether to overwrite existing rows. It defaults to use
// "data" as the key name unless one is provided
func WithOverwrite(overwrite bool, keyName string) WriteRowsOption {
	if keyName == "" {
		keyName = "data"
	}
	return func(c *writeRowsConfig) {
		c.overwrite = overwrite
		c.keyName = keyName
	}
}

// WithDryRun sets whether this is a dry run
func WithDryRun(dryRun bool) WriteRowsOption {
	return func(c *writeRowsConfig) {
		c.dryRun = dryRun
	}
}

// WriteRows writes the given rows to S3, serialized as JSON objects, one per line.
// It provides the following options:
//   - WithDryRun: sets whether this is a dry run (default false)
//   - WithOverwrite: sets whether to overwrite existing rows (default false)
//
// If a partition key is specified, then the value for that key must be a string
// in each row. The destination file for each row will be:
//   - s3://<bucket>/<keyPrefix>/<generated key name>
//     if there is no partition key specified (partitionKey is "")
//   - s3://<bucket>/<keyPrefix>/<partionKey>=<row[partitionKey]>/<generated key name>
//     if there is a partition key specified
func WriteRows(ctx context.Context, bucket, keyPrefix string, rows []map[string]interface{}, partitionKey string, opts ...WriteRowsOption) error {
	config := &writeRowsConfig{
		dryRun:    false,
		overwrite: false,
	}

	for _, opt := range opts {
		opt(config)
	}

	var uploader *manager.Uploader
	if !config.dryRun {
		cfg, err := awsconfig.LoadDefaultConfig(ctx)
		if err != nil {
			return fmt.Errorf("error loading config: %s", err)
		}
		s3client := s3.NewFromConfig(cfg)
		uploader = manager.NewUploader(s3client)
	}

	var keyName string
	if config.overwrite {
		keyName = config.keyName
	} else {
		keyName = time.Now().UTC().Format(time.RFC3339Nano)
	}

	// In this loop we iterate through the rows, serializing them and dispatching
	// them to S3 uploaders that buffer the data in the background.
	writers := map[string]io.WriteCloser{} // partitionKey -> writer
	uploaderWait := new(sync.WaitGroup)
	var uploaderError error // if there are uploader errors, this will hold an arbitrary one of them
	uploaderErrorMu := new(sync.Mutex)
	for idx, row := range rows {
		key := keyPrefix
		if partitionKey != "" {
			datum, ok := row[partitionKey].(string)
			if !ok {
				return fmt.Errorf("Row %d: does not have %q field or it is not a string", idx, partitionKey)
			}
			key = fmt.Sprintf("%s/%s=%s", key, partitionKey, datum)
		}
		key = fmt.Sprintf("%s/%s.jsonrows", key, keyName)
		writer := writers[key]
		if writer == nil {
			if config.dryRun {
				log.Printf("Will upload to s3://%s/%s", bucket, key)
				writer = discard
				writers[key] = writer
			} else {
				r, w := io.Pipe()
				writers[key] = w
				writer = w
				uploaderWait.Add(1)
				go func(bucket, key string, reader io.Reader) {
					log.Printf("Starting upload of s3://%s/%s", bucket, key)
					_, err := uploader.Upload(ctx, &s3.PutObjectInput{
						Bucket: &bucket,
						Key:    &key,
						Body:   reader,
					})
					if err != nil {
						// There are two possibilities now:
						// 1. This was the last write to this pipe. Set uploaderError so that
						//    the function will eventually return an error.
						// 2. There will be more writes to this pipe. Close the pipe with an
						//    error so that the writes fail and report the error.
						uploaderErrorMu.Lock()
						uploaderError = err
						uploaderErrorMu.Unlock()
						r.CloseWithError(fmt.Errorf("Uploader error: %s", err))
					} else {
						log.Printf("Finished upload of s3://%s/%s", bucket, key)
					}
					uploaderWait.Done()

				}(bucket, key, r)
			}
		}
		err := json.NewEncoder(writer).Encode(row)
		if err != nil {
			return fmt.Errorf("Row %d: error writing JSON: %s", idx, err)
		}
	}
	for _, w := range writers {
		w.Close()
	}
	uploaderWait.Wait()
	return uploaderError
}
