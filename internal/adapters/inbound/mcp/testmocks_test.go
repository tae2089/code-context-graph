package mcp

import (
	"context"

	flows "github.com/tae2089/code-context-graph/internal/app/analyze/flow"
	"github.com/tae2089/code-context-graph/internal/app/analyze/query"
	"github.com/tae2089/code-context-graph/internal/app/ingest/incremental"
	requestctx "github.com/tae2089/code-context-graph/internal/ctx"
	"github.com/tae2089/code-context-graph/internal/domain/graph"
)

type mockQueryService struct {
	callersOfCalled         bool
	calleesOfCalled         bool
	callersPageCalled       bool
	calleesPageCalled       bool
	callersWithOptions      bool
	calleesWithOptions      bool
	callersOfCalls          int
	calleesOfCalls          int
	callersPageCalls        int
	calleesPageCalls        int
	callersWithOptionsCalls int
	calleesWithOptionsCalls int
	callersPageOpts         query.QueryOptions
	calleesPageOpts         query.QueryOptions
	callersOpts             query.QueryOptions
	calleesOpts             query.QueryOptions
	importsOfCalled         bool
	importersOfCalled       bool
	importsOfPageCalled     bool
	importersOfPageCalled   bool
	importsOfCalls          int
	importersOfCalls        int
	importsOfPageCalls      int
	importersOfPageCalls    int
	importsOfPageOpts       query.QueryOptions
	importersOfPageOpts     query.QueryOptions
	childrenOfCalled        bool
	childrenOfPageCalled    bool
	childrenOfCalls         int
	childrenOfPageCalls     int
	childrenOfPageOpts      query.QueryOptions
	testsForCalled          bool
	testsForPageCalled      bool
	testsForCalls           int
	testsForPageCalls       int
	testsForPageOpts        query.QueryOptions
	inheritorsOfCalled      bool
	inheritorsOfPageCalled  bool
	inheritorsOfCalls       int
	inheritorsOfPageCalls   int
	inheritorsOfPageOpts    query.QueryOptions
	fileSummaryCalled       bool
	findMatchesCalled       bool
	result                  []graph.Node
	fileSummaryResult       *query.FileSummary
	matchResult             []query.CandidateMatch
	err                     error
}

func applyQueryPage(items []graph.Node, opts query.QueryOptions) query.PagedNodes {
	total := len(items)
	start := opts.Offset
	if start < 0 {
		start = 0
	}
	if start > total {
		start = total
	}
	window := append([]graph.Node(nil), items[start:]...)
	if opts.Limit > 0 && len(window) > opts.Limit {
		window = window[:opts.Limit]
	}
	return query.PagedNodes{Nodes: window, TotalCount: total}
}

func (m *mockQueryService) CallersOf(ctx context.Context, nodeID uint) ([]graph.Node, error) {
	m.callersOfCalled = true
	m.callersOfCalls++
	return m.result, m.err
}
func (m *mockQueryService) CallersOfPage(ctx context.Context, nodeID uint, opts query.QueryOptions) (query.PagedNodes, error) {
	m.callersPageCalled = true
	m.callersPageCalls++
	m.callersPageOpts = opts
	return applyQueryPage(m.result, opts), m.err
}
func (m *mockQueryService) CallersOfWithOptions(ctx context.Context, nodeID uint, opts query.QueryOptions) ([]graph.Node, error) {
	m.callersOfCalled = true
	m.callersWithOptions = true
	m.callersWithOptionsCalls++
	m.callersOfCalls++
	m.callersOpts = opts
	return m.result, m.err
}
func (m *mockQueryService) CalleesOf(ctx context.Context, nodeID uint) ([]graph.Node, error) {
	m.calleesOfCalled = true
	m.calleesOfCalls++
	return m.result, m.err
}
func (m *mockQueryService) CalleesOfPage(ctx context.Context, nodeID uint, opts query.QueryOptions) (query.PagedNodes, error) {
	m.calleesPageCalled = true
	m.calleesPageCalls++
	m.calleesPageOpts = opts
	return applyQueryPage(m.result, opts), m.err
}
func (m *mockQueryService) CalleesOfWithOptions(ctx context.Context, nodeID uint, opts query.QueryOptions) ([]graph.Node, error) {
	m.calleesOfCalled = true
	m.calleesWithOptions = true
	m.calleesWithOptionsCalls++
	m.calleesOfCalls++
	m.calleesOpts = opts
	return m.result, m.err
}
func (m *mockQueryService) ImportsOf(ctx context.Context, nodeID uint) ([]graph.Node, error) {
	m.importsOfCalled = true
	m.importsOfCalls++
	return m.result, m.err
}
func (m *mockQueryService) ImportsOfPage(ctx context.Context, nodeID uint, opts query.QueryOptions) (query.PagedNodes, error) {
	m.importsOfPageCalled = true
	m.importsOfPageCalls++
	m.importsOfPageOpts = opts
	return applyQueryPage(m.result, opts), m.err
}
func (m *mockQueryService) ImportersOf(ctx context.Context, nodeID uint) ([]graph.Node, error) {
	m.importersOfCalled = true
	m.importersOfCalls++
	return m.result, m.err
}
func (m *mockQueryService) ImportersOfPage(ctx context.Context, nodeID uint, opts query.QueryOptions) (query.PagedNodes, error) {
	m.importersOfPageCalled = true
	m.importersOfPageCalls++
	m.importersOfPageOpts = opts
	return applyQueryPage(m.result, opts), m.err
}
func (m *mockQueryService) ChildrenOf(ctx context.Context, nodeID uint) ([]graph.Node, error) {
	m.childrenOfCalled = true
	m.childrenOfCalls++
	return m.result, m.err
}
func (m *mockQueryService) ChildrenOfPage(ctx context.Context, nodeID uint, opts query.QueryOptions) (query.PagedNodes, error) {
	m.childrenOfPageCalled = true
	m.childrenOfPageCalls++
	m.childrenOfPageOpts = opts
	return applyQueryPage(m.result, opts), m.err
}
func (m *mockQueryService) TestsFor(ctx context.Context, nodeID uint) ([]graph.Node, error) {
	m.testsForCalled = true
	m.testsForCalls++
	return m.result, m.err
}
func (m *mockQueryService) TestsForPage(ctx context.Context, nodeID uint, opts query.QueryOptions) (query.PagedNodes, error) {
	m.testsForPageCalled = true
	m.testsForPageCalls++
	m.testsForPageOpts = opts
	return applyQueryPage(m.result, opts), m.err
}
func (m *mockQueryService) InheritorsOf(ctx context.Context, nodeID uint) ([]graph.Node, error) {
	m.inheritorsOfCalled = true
	m.inheritorsOfCalls++
	return m.result, m.err
}
func (m *mockQueryService) InheritorsOfPage(ctx context.Context, nodeID uint, opts query.QueryOptions) (query.PagedNodes, error) {
	m.inheritorsOfPageCalled = true
	m.inheritorsOfPageCalls++
	m.inheritorsOfPageOpts = opts
	return applyQueryPage(m.result, opts), m.err
}
func (m *mockQueryService) FileSummaryOf(ctx context.Context, filePath string) (*query.FileSummary, error) {
	m.fileSummaryCalled = true
	return m.fileSummaryResult, m.err
}

func (m *mockQueryService) FindExactNameMatches(ctx context.Context, target string, limit int) ([]query.CandidateMatch, error) {
	m.findMatchesCalled = true
	return m.matchResult, m.err
}

type mockFlowBuilder struct {
	rebuildCalled bool
	result        []flows.Stats
	err           error
}

func (m *mockFlowBuilder) Rebuild(ctx context.Context, cfg flows.Config) ([]flows.Stats, error) {
	m.rebuildCalled = true
	return m.result, m.err
}

type mockFlowTracer struct {
	calls        int
	opts         []flows.TraceOptions
	returnFlow   *graph.Flow
	traceErr     error
	remainderErr error
}

func (m *mockFlowTracer) TraceFlowBounded(ctx context.Context, startNodeID uint, opts flows.TraceOptions) (*flows.TraceResult, error) {
	m.calls++
	m.opts = append(m.opts, opts)
	flow := m.returnFlow
	if flow == nil {
		flow = &graph.Flow{
			Namespace: requestctx.FromContext(ctx),
			Name:      "flow_from_mock",
			Members: []graph.FlowMembership{{
				NodeID:    startNodeID,
				Ordinal:   0,
				Namespace: requestctx.FromContext(ctx),
			}},
		}
	}
	return &flows.TraceResult{
		Flow:          flow,
		Truncated:     false,
		MaxNodes:      opts.MaxNodes,
		ReturnedNodes: len(flow.Members),
	}, m.remainderErr
}

type mockIncrementalSyncer struct {
	syncCalled       bool
	syncWithExisting bool
	files            map[string]incremental.FileInfo
	existingFiles    []string
	filesCalls       []map[string]incremental.FileInfo
	existingCalls    [][]string
	result           *incremental.SyncStats
	err              error
}

func (m *mockIncrementalSyncer) Sync(ctx context.Context, files map[string]incremental.FileInfo) (*incremental.SyncStats, error) {
	m.syncCalled = true
	m.files = files
	return m.result, m.err
}

func (m *mockIncrementalSyncer) SyncWithExisting(ctx context.Context, files map[string]incremental.FileInfo, existingFiles []string) (*incremental.SyncStats, error) {
	m.syncWithExisting = true
	m.files = files
	m.existingFiles = append([]string(nil), existingFiles...)
	fileCopy := make(map[string]incremental.FileInfo, len(files))
	for k, v := range files {
		fileCopy[k] = v
	}
	m.filesCalls = append(m.filesCalls, fileCopy)
	m.existingCalls = append(m.existingCalls, append([]string(nil), existingFiles...))
	return m.result, m.err
}
