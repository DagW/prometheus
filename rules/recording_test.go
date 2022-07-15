// Copyright 2013 The Prometheus Authors
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
// http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package rules

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/prometheus/prometheus/model/labels"
	"github.com/prometheus/prometheus/model/timestamp"
	"github.com/prometheus/prometheus/promql"
	"github.com/prometheus/prometheus/promql/parser"
	"github.com/prometheus/prometheus/util/teststorage"
)

func TestRuleEval(t *testing.T) {
	storage := teststorage.New(t)
	defer storage.Close()

	opts := promql.EngineOpts{
		Logger:     nil,
		Reg:        nil,
		MaxSamples: 10,
		Timeout:    10 * time.Second,
	}

	engine := promql.NewEngine(opts)
	ctx, cancelCtx := context.WithCancel(context.Background())
	defer cancelCtx()

	now := time.Now()

	suite := []struct {
		name      string
		expr      parser.Expr
		labels    labels.Labels
		queryFunc QueryFunc
		result    promql.Vector
		err       string
	}{
		{
			name:      "nolabels",
			expr:      &parser.NumberLiteral{Val: 1},
			labels:    labels.Labels{},
			queryFunc: EngineQueryFunc(engine, storage),
			result: promql.Vector{promql.Sample{
				Metric: labels.FromStrings("__name__", "nolabels"),
				Point:  promql.Point{V: 1, T: timestamp.FromTime(now)},
			}},
		},
		{
			name:      "labels",
			expr:      &parser.NumberLiteral{Val: 1},
			labels:    labels.FromStrings("foo", "bar"),
			queryFunc: EngineQueryFunc(engine, storage),
			result: promql.Vector{promql.Sample{
				Metric: labels.FromStrings("__name__", "labels", "foo", "bar"),
				Point:  promql.Point{V: 1, T: timestamp.FromTime(now)},
			}},
		},
		{
			name:   "query processing error",
			expr:   &parser.NumberLiteral{Val: 1},
			labels: labels.FromStrings("foo", "bar"),
			queryFunc: func(ctx context.Context, q string, t time.Time) (promql.Vector, error) {
				return nil, fmt.Errorf("dummy")
			},
			result: nil,
			err:    "dummy",
		},
	}

	for _, test := range suite {
		rule := NewRecordingRule(test.name, test.expr, test.labels)
		result, err := rule.Eval(ctx, now, test.queryFunc, nil, 0)
		if test.err == "" {
			require.NoError(t, err)
		} else {
			require.Equal(t, test.err, err.Error())
		}
		require.Equal(t, test.result, result)
	}
}

// TestRuleEvalDuplicate tests for duplicate labels in recorded metrics, see #5529.
func TestRuleEvalDuplicate(t *testing.T) {
	storage := teststorage.New(t)
	defer storage.Close()

	opts := promql.EngineOpts{
		Logger:     nil,
		Reg:        nil,
		MaxSamples: 10,
		Timeout:    10 * time.Second,
	}

	engine := promql.NewEngine(opts)
	ctx, cancelCtx := context.WithCancel(context.Background())
	defer cancelCtx()

	now := time.Now()

	expr, err := parser.ParseExpr(`vector(0) or label_replace(vector(0),"test","x","","")`)
	require.NoError(t, err)
	rule := NewRecordingRule("foo", expr, labels.FromStrings("test", "test"))
	_, err = rule.Eval(ctx, now, EngineQueryFunc(engine, storage), nil, 0)
	require.Error(t, err)
	require.EqualError(t, err, "vector contains metrics with the same labelset after applying rule labels")
}

func TestRecordingRuleLimit(t *testing.T) {
	suite, err := promql.NewTest(t, `
		load 1m
			metric{label="1"} 1
			metric{label="2"} 1
	`)
	require.NoError(t, err)
	defer suite.Close()

	require.NoError(t, suite.Run())

	tests := []struct {
		limit int
		err   string
	}{
		{
			limit: 0,
		},
		{
			limit: -1,
		},
		{
			limit: 2,
		},
		{
			limit: 1,
			err:   "exceeded limit of 1 with 2 series",
		},
	}

	expr, err := parser.ParseExpr(`metric > 0`)
	require.NoError(t, err)
	rule := NewRecordingRule(
		"foo",
		expr,
		labels.FromStrings("test", "test"),
	)

	evalTime := time.Unix(0, 0)

	for _, test := range tests {
		_, err := rule.Eval(suite.Context(), evalTime, EngineQueryFunc(suite.QueryEngine(), suite.Storage()), nil, test.limit)
		if err != nil {
			require.EqualError(t, err, test.err)
		} else if test.err != "" {
			t.Errorf("Expected error %s, got none", test.err)
		}
	}
}

func TestNewRecordingRule(t *testing.T) {
	name := "name"
	labels := labels.FromStrings("test", "test")
	query, err := parser.ParseExpr(`metric > 0`)
	require.NoError(t, err)

	recordingRule := NewRecordingRule(name, query, labels)
	require.NotNil(t, recordingRule)

	require.Equal(t, name, recordingRule.Name())
	require.Equal(t, labels, recordingRule.Labels())
	require.Equal(t, query, recordingRule.Query())
	require.Equal(t, "record: name\nexpr: metric > 0\nlabels:\n  test: test\n", recordingRule.String())

	ts := time.Now()
	recordingRule.SetEvaluationTimestamp(ts)
	require.Equal(t, ts, recordingRule.GetEvaluationTimestamp())

	duration := time.Second
	recordingRule.SetEvaluationDuration(duration)
	require.Equal(t, duration, recordingRule.GetEvaluationDuration())

	recordingRule.SetHealth(HealthGood)
	require.Equal(t, HealthGood, recordingRule.Health())

	testError := fmt.Errorf("dummy")
	recordingRule.SetLastError(testError)
	require.Equal(t, testError, recordingRule.LastError())
}
