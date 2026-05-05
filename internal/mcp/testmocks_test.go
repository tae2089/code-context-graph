package mcp

import (
	"context"

	"github.com/tae2089/code-context-graph/internal/analysis/community"
	"github.com/tae2089/code-context-graph/internal/analysis/coupling"
	"github.com/tae2089/code-context-graph/internal/analysis/coverage"
	"github.com/tae2089/code-context-graph/internal/analysis/deadcode"
	fallbackanalysis "github.com/tae2089/code-context-graph/internal/analysis/fallback"
	"github.com/tae2089/code-context-graph/internal/analysis/flows"
	"github.com/tae2089/code-context-graph/internal/analysis/incremental"
	"github.com/tae2089/code-context-graph/internal/analysis/query"
	"github.com/tae2089/code-context-graph/internal/model"
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
	importsOfPageCalled      bool
	importersOfPageCalled    bool
	importsOfCalls          int
	importersOfCalls        int
	importsOfPageCalls      int
	importersOfPageCalls    int
	importsOfPageOpts       query.QueryOptions
	importersOfPageOpts     query.QueryOptions
	childrenOfCalled        bool
	childrenOfPageCalled     bool
	childrenOfCalls          int
	childrenOfPageCalls      int
	childrenOfPageOpts       query.QueryOptions
	testsForCalled          bool
	testsForPageCalled       bool
	testsForCalls            int
	testsForPageCalls        int
	testsForPageOpts         query.QueryOptions
	inheritorsOfCalled      bool
	inheritorsOfPageCalled   bool
	inheritorsOfCalls        int
	inheritorsOfPageCalls    int
	inheritorsOfPageOpts     query.QueryOptions
	fileSummaryCalled       bool
	findMatchesCalled       bool
	result                  []model.Node
	fileSummaryResult       *query.FileSummary
	matchResult             []query.CandidateMatch
	err                     error
}

func (m *mockQueryService) CallersOf(ctx context.Context, nodeID uint) ([]model.Node, error) {
	m.callersOfCalled = true
	m.callersOfCalls++
	return m.result, m.err
}
func (m *mockQueryService) CallersOfPage(ctx context.Context, nodeID uint, opts query.QueryOptions) (query.PagedNodes, error) {
	m.callersPageCalled = true
	m.callersPageCalls++
	m.callersPageOpts = opts
	return query.PagedNodes{Nodes: m.result, TotalCount: len(m.result)}, m.err
}
func (m *mockQueryService) CallersOfWithOptions(ctx context.Context, nodeID uint, opts query.QueryOptions) ([]model.Node, error) {
	m.callersOfCalled = true
	m.callersWithOptions = true
	m.callersWithOptionsCalls++
	m.callersOfCalls++
	m.callersOpts = opts
	return m.result, m.err
}
func (m *mockQueryService) CalleesOf(ctx context.Context, nodeID uint) ([]model.Node, error) {
	m.calleesOfCalled = true
	m.calleesOfCalls++
	return m.result, m.err
}
func (m *mockQueryService) CalleesOfPage(ctx context.Context, nodeID uint, opts query.QueryOptions) (query.PagedNodes, error) {
	m.calleesPageCalled = true
	m.calleesPageCalls++
	m.calleesPageOpts = opts
	return query.PagedNodes{Nodes: m.result, TotalCount: len(m.result)}, m.err
}
func (m *mockQueryService) CalleesOfWithOptions(ctx context.Context, nodeID uint, opts query.QueryOptions) ([]model.Node, error) {
	m.calleesOfCalled = true
	m.calleesWithOptions = true
	m.calleesWithOptionsCalls++
	m.calleesOfCalls++
	m.calleesOpts = opts
	return m.result, m.err
}
func (m *mockQueryService) ImportsOf(ctx context.Context, nodeID uint) ([]model.Node, error) {
	m.importsOfCalled = true
	m.importsOfCalls++
	return m.result, m.err
}
func (m *mockQueryService) ImportsOfPage(ctx context.Context, nodeID uint, opts query.QueryOptions) (query.PagedNodes, error) {
	m.importsOfPageCalled = true
	m.importsOfPageCalls++
	m.importsOfPageOpts = opts
	return query.PagedNodes{Nodes: m.result, TotalCount: len(m.result)}, m.err
}
func (m *mockQueryService) ImportersOf(ctx context.Context, nodeID uint) ([]model.Node, error) {
	m.importersOfCalled = true
	m.importersOfCalls++
	return m.result, m.err
}
func (m *mockQueryService) ImportersOfPage(ctx context.Context, nodeID uint, opts query.QueryOptions) (query.PagedNodes, error) {
	m.importersOfPageCalled = true
	m.importersOfPageCalls++
	m.importersOfPageOpts = opts
	return query.PagedNodes{Nodes: m.result, TotalCount: len(m.result)}, m.err
}
func (m *mockQueryService) ChildrenOf(ctx context.Context, nodeID uint) ([]model.Node, error) {
	m.childrenOfCalled = true
	m.childrenOfCalls++
	return m.result, m.err
}
func (m *mockQueryService) ChildrenOfPage(ctx context.Context, nodeID uint, opts query.QueryOptions) (query.PagedNodes, error) {
	m.childrenOfPageCalled = true
	m.childrenOfPageCalls++
	m.childrenOfPageOpts = opts
	return query.PagedNodes{Nodes: m.result, TotalCount: len(m.result)}, m.err
}
func (m *mockQueryService) TestsFor(ctx context.Context, nodeID uint) ([]model.Node, error) {
	m.testsForCalled = true
	m.testsForCalls++
	return m.result, m.err
}
func (m *mockQueryService) TestsForPage(ctx context.Context, nodeID uint, opts query.QueryOptions) (query.PagedNodes, error) {
	m.testsForPageCalled = true
	m.testsForPageCalls++
	m.testsForPageOpts = opts
	return query.PagedNodes{Nodes: m.result, TotalCount: len(m.result)}, m.err
}
func (m *mockQueryService) InheritorsOf(ctx context.Context, nodeID uint) ([]model.Node, error) {
	m.inheritorsOfCalled = true
	m.inheritorsOfCalls++
	return m.result, m.err
}
func (m *mockQueryService) InheritorsOfPage(ctx context.Context, nodeID uint, opts query.QueryOptions) (query.PagedNodes, error) {
	m.inheritorsOfPageCalled = true
	m.inheritorsOfPageCalls++
	m.inheritorsOfPageOpts = opts
	return query.PagedNodes{Nodes: m.result, TotalCount: len(m.result)}, m.err
}
func (m *mockQueryService) FileSummaryOf(ctx context.Context, filePath string) (*query.FileSummary, error) {
	m.fileSummaryCalled = true
	return m.fileSummaryResult, m.err
}

func (m *mockQueryService) FindExactNameMatches(ctx context.Context, target string, limit int) ([]query.CandidateMatch, error) {
	m.findMatchesCalled = true
	return m.matchResult, m.err
}

type mockLargefuncAnalyzer struct {
	findCalled bool
	result     []model.Node
	err        error
}

func (m *mockLargefuncAnalyzer) Find(ctx context.Context, threshold int) ([]model.Node, error) {
	m.findCalled = true
	return m.result, m.err
}

type mockDeadcodeAnalyzer struct {
	findCalled bool
	result     []model.Node
	err        error
}

func (m *mockDeadcodeAnalyzer) Find(ctx context.Context, opts deadcode.Options) ([]model.Node, error) {
	m.findCalled = true
	return m.result, m.err
}

type mockFallbackAnalyzer struct {
	findCalled bool
	result     []fallbackanalysis.SuspectEdge
	err        error
}

func (m *mockFallbackAnalyzer) FindSuspects(ctx context.Context, opts fallbackanalysis.Options) ([]fallbackanalysis.SuspectEdge, error) {
	m.findCalled = true
	return m.result, m.err
}

type mockCouplingAnalyzer struct {
	analyzeCalled bool
	result        []coupling.CouplingPair
	err           error
}

func (m *mockCouplingAnalyzer) Analyze(ctx context.Context) ([]coupling.CouplingPair, error) {
	m.analyzeCalled = true
	return m.result, m.err
}

type mockCoverageAnalyzer struct {
	byFileCalled    bool
	byCommunCalled  bool
	fileResult      *coverage.FileCoverage
	communityResult *coverage.CommunityCoverage
	err             error
}

func (m *mockCoverageAnalyzer) ByFile(ctx context.Context, filePath string) (*coverage.FileCoverage, error) {
	m.byFileCalled = true
	return m.fileResult, m.err
}
func (m *mockCoverageAnalyzer) ByCommunity(ctx context.Context, communityID uint) (*coverage.CommunityCoverage, error) {
	m.byCommunCalled = true
	return m.communityResult, m.err
}

type mockCommunityBuilder struct {
	rebuildCalled bool
	result        []community.Stats
	err           error
}

func (m *mockCommunityBuilder) Rebuild(ctx context.Context, cfg community.Config) ([]community.Stats, error) {
	m.rebuildCalled = true
	return m.result, m.err
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
