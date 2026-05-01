package mcp

import (
	"context"

	"github.com/tae2089/code-context-graph/internal/analysis/community"
	"github.com/tae2089/code-context-graph/internal/analysis/coupling"
	"github.com/tae2089/code-context-graph/internal/analysis/coverage"
	"github.com/tae2089/code-context-graph/internal/analysis/deadcode"
	"github.com/tae2089/code-context-graph/internal/analysis/incremental"
	"github.com/tae2089/code-context-graph/internal/analysis/query"
	"github.com/tae2089/code-context-graph/internal/model"
)

type mockQueryService struct {
	callersOfCalled    bool
	calleesOfCalled    bool
	importsOfCalled    bool
	importersOfCalled  bool
	childrenOfCalled   bool
	testsForCalled     bool
	inheritorsOfCalled bool
	fileSummaryCalled  bool
	result             []model.Node
	fileSummaryResult  *query.FileSummary
	err                error
}

func (m *mockQueryService) CallersOf(ctx context.Context, nodeID uint) ([]model.Node, error) {
	m.callersOfCalled = true
	return m.result, m.err
}
func (m *mockQueryService) CalleesOf(ctx context.Context, nodeID uint) ([]model.Node, error) {
	m.calleesOfCalled = true
	return m.result, m.err
}
func (m *mockQueryService) ImportsOf(ctx context.Context, nodeID uint) ([]model.Node, error) {
	m.importsOfCalled = true
	return m.result, m.err
}
func (m *mockQueryService) ImportersOf(ctx context.Context, nodeID uint) ([]model.Node, error) {
	m.importersOfCalled = true
	return m.result, m.err
}
func (m *mockQueryService) ChildrenOf(ctx context.Context, nodeID uint) ([]model.Node, error) {
	m.childrenOfCalled = true
	return m.result, m.err
}
func (m *mockQueryService) TestsFor(ctx context.Context, nodeID uint) ([]model.Node, error) {
	m.testsForCalled = true
	return m.result, m.err
}
func (m *mockQueryService) InheritorsOf(ctx context.Context, nodeID uint) ([]model.Node, error) {
	m.inheritorsOfCalled = true
	return m.result, m.err
}
func (m *mockQueryService) FileSummaryOf(ctx context.Context, filePath string) (*query.FileSummary, error) {
	m.fileSummaryCalled = true
	return m.fileSummaryResult, m.err
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

type mockIncrementalSyncer struct {
	syncCalled       bool
	syncWithExisting bool
	files            map[string]incremental.FileInfo
	existingFiles    []string
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
	return m.result, m.err
}
