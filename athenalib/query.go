package athenalib

import (
	"context"
	"fmt"
	"log"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/athena"
	"github.com/aws/aws-sdk-go-v2/service/athena/types"
)

func waitForQuery(ctx context.Context, ath *athena.Client, execID *string, logDetails bool) error {
	for {
		out, err := ath.GetQueryExecution(ctx, &athena.GetQueryExecutionInput{
			QueryExecutionId: execID,
		})
		if err != nil {
			return fmt.Errorf("Getting query status failed: %s", err)
		}
		status := string(out.QueryExecution.Status.State)
		switch status {
		case "SUCCEEDED":
			return nil
		case "QUEUED":
			if logDetails {
				log.Printf("Query is queued")
			}
		case "RUNNING":
			if logDetails {
				log.Printf("Query is running")
			}
		case "FAILED":
			return fmt.Errorf("Query return status %q: %s", status, aws.ToString(out.QueryExecution.Status.StateChangeReason))
		case "CANCELLED":
			return fmt.Errorf("Query return status %q", status)
		default:
			return fmt.Errorf("Unknown query status %q", status)
		}
		time.Sleep(time.Second)
	}
}

func QueryAthena(ctx context.Context, ath *athena.Client, database, query, outputLocation string, maxRows int64, logDetails bool) ([]types.Row, error) {
	execIn := &athena.StartQueryExecutionInput{
		QueryString: aws.String(query),
		ResultConfiguration: &types.ResultConfiguration{
			OutputLocation: &outputLocation,
		},
	}
	if database != "" {
		execIn.QueryExecutionContext = &types.QueryExecutionContext{
			Database: &database,
		}
	}
	execOut, err := ath.StartQueryExecution(ctx, execIn)
	if err != nil {
		return nil, fmt.Errorf("Querying failed: %s", err)
	}

	err = waitForQuery(ctx, ath, execOut.QueryExecutionId, logDetails)
	if err != nil {
		return nil, fmt.Errorf("Waiting for query results failed: %s", err)
	}

	var rowsPerQuery int64 = 1000
	if maxRows > 0 && maxRows < rowsPerQuery {
		rowsPerQuery = maxRows
	}

	var rows []types.Row
	var nextToken *string = nil

	for {
		var numRows int64 = 1000
		if maxRows > 0 && int64(len(rows))+numRows > maxRows {
			numRows = maxRows - int64(len(rows))
		}
		out, err := ath.GetQueryResults(ctx, &athena.GetQueryResultsInput{
			QueryExecutionId: execOut.QueryExecutionId,
			MaxResults:       aws.Int32(int32(numRows)),
			NextToken:        nextToken,
		})
		if err != nil {
			return nil, fmt.Errorf("Getting results failed: %s", err)
		}

		rows = append(rows, out.ResultSet.Rows...)
		if maxRows > 0 && maxRows <= int64(len(rows)) {
			break
		}
		nextToken = out.NextToken
		if nextToken == nil {
			break
		}
	}
	return rows, nil
}
