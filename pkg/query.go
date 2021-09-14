package main

import (
	"fmt"
	"math"
	"strconv"
	"time"

	"github.com/aws/aws-sdk-go/aws"
	"github.com/aws/aws-sdk-go/aws/awserr"
	"github.com/aws/aws-sdk-go/service/athena"
	"github.com/grafana/grafana-plugin-sdk-go/backend"
	"github.com/patrickmn/go-cache"
	"github.com/prometheus/client_golang/prometheus"
	"golang.org/x/net/context"
)

type AwsAthenaQuery struct {
	client                *athena.Athena
	cache                 *cache.Cache
	metrics               *AwsAthenaMetrics
	datasourceID          int64
	waitQueryExecutionIds []*string
	RefId                 string
	Region                string
	Inputs                []athena.GetQueryResultsInput
	TimestampColumn       string
	ValueColumn           string
	LegendFormat          string
	TimeFormat            string
	MaxRows               string
	CacheDuration         Duration
	WorkGroup             string
	QueryString           string
	OutputLocation        string
	From                  time.Time
	To                    time.Time
}

func (query *AwsAthenaQuery) getQueryResults(ctx context.Context, pluginContext backend.PluginContext) (*athena.GetQueryResultsOutput, error) {
	var err error

	if query.QueryString == "" {
		dedupe := true // TODO: add query option?
		if dedupe {
			allQueryExecution := make([]*athena.QueryExecution, 0)
			for i := 0; i < len(query.Inputs); i += AWS_API_RESULT_MAX_LENGTH {
				e := int64(math.Min(float64(i+AWS_API_RESULT_MAX_LENGTH), float64(len(query.Inputs))))
				bi := &athena.BatchGetQueryExecutionInput{}
				for _, input := range query.Inputs[i:e] {
					bi.QueryExecutionIds = append(bi.QueryExecutionIds, input.QueryExecutionId)
				}
				bo, err := query.client.BatchGetQueryExecutionWithContext(ctx, bi)
				if aerr, ok := err.(awserr.Error); ok && aerr.Code() == athena.ErrCodeInvalidRequestException {
					backend.Logger.Warn("Batch Get Query Execution Warning", "warn", aerr.Message())
					bo = &athena.BatchGetQueryExecutionOutput{QueryExecutions: make([]*athena.QueryExecution, 0)}
				} else if err != nil {
					return nil, err
				}
				allQueryExecution = append(allQueryExecution, bo.QueryExecutions...)
			}

			dupCheck := make(map[string]bool)
			query.Inputs = make([]athena.GetQueryResultsInput, 0)
			for _, q := range allQueryExecution {
				if _, dup := dupCheck[*q.Query]; dup {
					continue
				}
				dupCheck[*q.Query] = true
				query.Inputs = append(query.Inputs, athena.GetQueryResultsInput{
					QueryExecutionId: q.QueryExecutionId,
				})
			}
		}
	} else {
		backend.Logger.Debug("Getting workgroup", "queryString", query.QueryString)
		workgroup, err := query.getWorkgroup(ctx, pluginContext, query.Region, query.WorkGroup)
		if err != nil {
			return nil, err
		}
		if workgroup.WorkGroup.Configuration.BytesScannedCutoffPerQuery == nil {
			return nil, fmt.Errorf("should set scan data limit")
		}

		backend.Logger.Debug("Starting query execution", "queryString", query.QueryString)
		queryExecutionID, err := query.startQueryExecution(ctx)
		if err != nil {
			return nil, err
		}
		backend.Logger.Debug("Started query execution", "queryString", query.QueryString, "queryExecutionID", aws.String(queryExecutionID))

		query.Inputs = append(query.Inputs, athena.GetQueryResultsInput{
			QueryExecutionId: aws.String(queryExecutionID),
		})
	}

	// wait until query completed
	backend.Logger.Debug("Waiting for queries", "len", len(query.waitQueryExecutionIds))
	if len(query.waitQueryExecutionIds) > 0 {
		if err := query.waitForQueryCompleted(ctx, query.waitQueryExecutionIds); err != nil {
			return nil, err
		}
	}

	maxRows := int64(DEFAULT_MAX_ROWS)
	if query.MaxRows != "" {
		maxRows, err = strconv.ParseInt(query.MaxRows, 10, 64)
		if err != nil {
			return nil, err
		}
	}

	backend.Logger.Debug("Getting results output")
	result := athena.GetQueryResultsOutput{
		ResultSet: &athena.ResultSet{
			Rows: make([]*athena.Row, 0),
			ResultSetMetadata: &athena.ResultSetMetadata{
				ColumnInfo: make([]*athena.ColumnInfo, 0),
			},
		},
	}
	for _, input := range query.Inputs {
		var resp *athena.GetQueryResultsOutput

		cacheKey := "QueryResults/" + strconv.FormatInt(pluginContext.DataSourceInstanceSettings.ID, 10) + "/" + query.Region + "/" + *input.QueryExecutionId + "/" + query.MaxRows
		if item, _, found := query.cache.GetWithExpiration(cacheKey); found && query.CacheDuration > 0 {
			backend.Logger.Debug("Returning results from cache", "queryExecutionID", input.QueryExecutionId)
			if r, ok := item.(*athena.GetQueryResultsOutput); ok {
				resp = r
			}
		} else {
			backend.Logger.Debug("Getting query results", "queryExecutionID", input.QueryExecutionId)
			err := query.client.GetQueryResultsPagesWithContext(ctx, &input,
				func(page *athena.GetQueryResultsOutput, lastPage bool) bool {
					query.metrics.queriesTotal.With(prometheus.Labels{"region": query.Region}).Inc()
					if resp == nil {
						resp = page
					} else {
						resp.ResultSet.Rows = append(resp.ResultSet.Rows, page.ResultSet.Rows...)
					}
					// result include extra header row, +1 here
					if maxRows != -1 && int64(len(resp.ResultSet.Rows)) > maxRows+1 {
						resp.ResultSet.Rows = resp.ResultSet.Rows[0 : maxRows+1]
						return false
					}
					return !lastPage
				})
			if aerr, ok := err.(awserr.Error); ok && aerr.Code() == athena.ErrCodeInvalidRequestException {
				backend.Logger.Warn("Get Query Results Warning", "warn", aerr.Message())
			} else if err != nil {
				backend.Logger.Debug("Get Query Results Warning", "warn", err)
				return nil, err
			}

			if query.CacheDuration > 0 {
				query.cache.Set(cacheKey, resp, time.Duration(query.CacheDuration)*time.Second)
			}
		}

		if resp == nil {
			continue
		}

		result.ResultSet.ResultSetMetadata = resp.ResultSet.ResultSetMetadata
		result.ResultSet.Rows = append(result.ResultSet.Rows, resp.ResultSet.Rows[1:]...) // trim header row
	}

	backend.Logger.Debug("Returning result", "rows", result.ResultSet.Rows)
	return &result, nil
}

func (query *AwsAthenaQuery) getWorkgroup(ctx context.Context, pluginContext backend.PluginContext, region string, workGroup string) (*athena.GetWorkGroupOutput, error) {
	WorkgroupCacheKey := "Workgroup/" + strconv.FormatInt(pluginContext.DataSourceInstanceSettings.ID, 10) + "/" + region + "/" + workGroup
	if item, _, found := query.cache.GetWithExpiration(WorkgroupCacheKey); found {
		if workgroup, ok := item.(*athena.GetWorkGroupOutput); ok {
			return workgroup, nil
		}
	}
	workgroup, err := query.client.GetWorkGroupWithContext(ctx, &athena.GetWorkGroupInput{WorkGroup: aws.String(workGroup)})
	if err != nil {
		return nil, err
	}
	query.cache.Set(WorkgroupCacheKey, workgroup, time.Duration(5)*time.Minute)

	return workgroup, nil
}

func (query *AwsAthenaQuery) startQueryExecution(ctx context.Context) (string, error) {
	// cache instant query result by query string
	var queryExecutionID string
	cacheKey := "StartQueryExecution/" + strconv.FormatInt(query.datasourceID, 10) + "/" + query.Region + "/" + query.QueryString + "/" + query.MaxRows
	if item, _, found := query.cache.GetWithExpiration(cacheKey); found && query.CacheDuration > 0 {
		if id, ok := item.(string); ok {
			queryExecutionID = id
		}
	} else {
		si := &athena.StartQueryExecutionInput{
			QueryString: aws.String(query.QueryString),
			WorkGroup:   aws.String(query.WorkGroup),
			ResultConfiguration: &athena.ResultConfiguration{
				OutputLocation: aws.String(query.OutputLocation),
			},
		}
		so, err := query.client.StartQueryExecutionWithContext(ctx, si)
		if err != nil {
			return "", err
		}
		queryExecutionID = *so.QueryExecutionId
		if query.CacheDuration > 0 {
			query.cache.Set(cacheKey, queryExecutionID, time.Duration(query.CacheDuration)*time.Second)
		}
		query.waitQueryExecutionIds = append(query.waitQueryExecutionIds, &queryExecutionID)
	}
	return queryExecutionID, nil
}

func (query *AwsAthenaQuery) waitForQueryCompleted(ctx context.Context, waitQueryExecutionIds []*string) error {
	backend.Logger.Debug("Waiting for queries", "len", len(waitQueryExecutionIds))

	for i := 0; i < QUERY_WAIT_COUNT; i++ {
		completeCount := 0

		backend.Logger.Debug("Getting execution status")
		bi := &athena.BatchGetQueryExecutionInput{QueryExecutionIds: waitQueryExecutionIds}
		bo, err := query.client.BatchGetQueryExecutionWithContext(ctx, bi)
		if err != nil {
			if err.Error() != "Query has not yet finished. Current state: RUNNING" {
				backend.Logger.Warn("Get execution status warning", "warn", err)
				return err
			}
		} else {
			for _, e := range bo.QueryExecutions {
				backend.Logger.Debug("Got execution status", "queryExecutionID", e.QueryExecutionId, "status", *e.Status.State)
				if !(*e.Status.State == "QUEUED" || *e.Status.State == "RUNNING") {
					completeCount++
				}

				if *e.Status.State == "CANCELLED" || *e.Status.State == "FAILED" {
					backend.Logger.Warn("Query did not succeed", "queryExecutionID", e.QueryExecutionId, "status", *e.Status.State)
				}
			}
		}
		if len(waitQueryExecutionIds) == completeCount {
			for _, e := range bo.QueryExecutions {
				query.metrics.dataScannedBytesTotal.With(prometheus.Labels{"region": query.Region}).Add(float64(*e.Statistics.DataScannedInBytes))
			}
			break
		} else {
			backend.Logger.Debug("Sleeping", "expected", len(waitQueryExecutionIds), "actual", completeCount)
			time.Sleep(1 * time.Second)
		}
	}
	return nil
}
