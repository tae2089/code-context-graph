import { lazy, Suspense, useEffect, useMemo, useState } from "react";
import ReactMarkdown from "react-markdown";
import {
  AlertCircle,
  Braces,
  Check,
  ChevronDown,
  ChevronRight,
  Code2,
  Copy,
  FileCode2,
  FileText,
  Folder,
  FolderTree,
  KeyRound,
  Loader2,
  Package,
  RefreshCw,
  Search,
  X
} from "lucide-react";
import {
  APIError,
  GraphNode,
  NodeDetails,
  SearchResult as DocSearchResult,
  TreeNode,
  buildContext,
  getDoc,
  getTree,
  listNamespaces,
  retrieveDocs,
  searchDocs
} from "./api";

const GraphView = lazy(() => import("./GraphView").then((module) => ({ default: module.GraphView })));

// @intent keep the minimal tree/search item data needed by the document viewer and context tray.
type SelectedDoc = {
  path: string;
  label: string;
  kind: string;
  summary: string;
  score?: number;
  matchedTerms?: string[];
  evidence?: RetrieveEvidence[];
  details?: NodeDetails;
};

// @intent preserve the tree nodes that caused a Retrieve result to rank.
type RetrieveEvidence = {
  id: string;
  path: string;
  label: string;
  kind: string;
  summary: string;
  treePath: string[];
};

const tokenStorageKey = "ccg-wiki-token";

// @intent constrain the Wiki search control to keyword tree search or PageIndex retrieval.
type SearchMode = "search" | "retrieve";

// @intent switch the center work area between generated docs and the visual edge graph.
type ViewMode = "docs" | "graph";

// @intent render the ccg-server Wiki workbench for tree navigation, Markdown viewing, and context copying.
export default function App() {
  const [token, setToken] = useState(() => localStorage.getItem(tokenStorageKey) || "");
  const [pendingToken, setPendingToken] = useState(token);
  const [namespaces, setNamespaces] = useState<string[]>([]);
  const [namespace, setNamespace] = useState("default");
  const [tree, setTree] = useState<TreeNode | null>(null);
  const [docPath, setDocPath] = useState("");
  const [docContent, setDocContent] = useState("");
  const [docError, setDocError] = useState("");
  const [searchQuery, setSearchQuery] = useState("");
  const [searchMode, setSearchMode] = useState<SearchMode>("search");
  const [viewMode, setViewMode] = useState<ViewMode>("docs");
  const [searchResults, setSearchResults] = useState<SelectedDoc[]>([]);
  const [selected, setSelected] = useState<SelectedDoc[]>([]);
  const [contextMarkdown, setContextMarkdown] = useState("");
  const [loading, setLoading] = useState(false);
  const [error, setError] = useState("");
  const [needsToken, setNeedsToken] = useState(false);
  const [copied, setCopied] = useState(false);

  useEffect(() => {
    void refresh();
  }, [token]);

  useEffect(() => {
    if (namespace) {
      void loadTree(namespace);
    }
  }, [namespace]);

  useEffect(() => {
    if (!searchQuery.trim()) {
      setSearchResults([]);
      return;
    }
    const timer = window.setTimeout(() => {
      void runSearch(searchQuery);
    }, 250);
    return () => window.clearTimeout(timer);
  }, [searchQuery, searchMode, namespace, token]);

  const tokenEstimate = useMemo(() => Math.ceil((contextMarkdown.length || selectedText(selected).length) / 4), [contextMarkdown, selected]);

  // @intent reload namespace choices and recover from token changes.
  async function refresh() {
    setLoading(true);
    setError("");
    try {
      const res = await listNamespaces(token);
      setNamespaces(res.namespaces);
      setNeedsToken(false);
      if (!res.namespaces.includes(namespace)) {
        setNamespace(res.namespaces[0] || "default");
      }
    } catch (err) {
      handleError(err);
    } finally {
      setLoading(false);
    }
  }

  // @intent load the active namespace's RAG tree into the left navigator.
  async function loadTree(ns: string) {
    setLoading(true);
    setError("");
    try {
      const res = await getTree(ns, token);
      setTree(res.root);
      setNeedsToken(false);
    } catch (err) {
      setTree(null);
      handleError(err);
    } finally {
      setLoading(false);
    }
  }

  // @intent open a selected tree/search item in the Markdown viewer.
  async function openDoc(item: SelectedDoc) {
    setDocPath(item.path);
    setDocError("");
    setDocContent("");
    if (!item.path) {
      setDocContent(symbolDetailMarkdown(item));
      return;
    }
    try {
      const res = await getDoc(namespace, item.path, token);
      setDocContent(res.content);
      setNeedsToken(false);
    } catch (err) {
      if (err instanceof APIError && err.status === 404) {
        setDocError("No generated Markdown file was found for this tree item.");
      } else {
        handleError(err);
      }
    }
  }

  // @intent update search results for the active namespace.
  async function runSearch(query: string) {
    try {
      if (searchMode === "retrieve") {
        const res = await retrieveDocs(namespace, query, token);
        setSearchResults(res.results.map((item) => ({
          path: item.doc_path || "",
          label: item.label,
          kind: normalizeKind(item.kind, item.id, item.doc_path),
          summary: retrieveSummary(item.summary, item.matched_terms, item.score, item.matches?.length || 0),
          score: item.score,
          matchedTerms: item.matched_terms,
          evidence: retrieveEvidence(item.matches),
          details: item.details
        })));
      } else {
        const res = await searchDocs(namespace, query, token);
        setSearchResults(res.results.map((item) => ({
          path: item.doc_path || "",
          label: item.label,
          kind: normalizeKind(item.kind, item.id, item.doc_path),
          summary: item.summary,
          details: item.details
        })));
      }
      setNeedsToken(false);
    } catch (err) {
      handleError(err);
    }
  }

  // @intent copy selected docs or summaries as one LLM-ready Markdown context block.
  async function copyContext() {
    const paths = selected.map((item) => item.path).filter(Boolean);
    const serverSections = new Map<string, string[]>();
    if (paths.length > 0) {
      try {
        const res = await buildContext(namespace, paths, token);
        for (const item of res.items) {
          if (!item.markdown?.trim()) {
            continue;
          }
          const existing = serverSections.get(item.path) || [];
          existing.push(item.markdown);
          serverSections.set(item.path, existing);
        }
      } catch (err) {
        handleError(err);
      }
    }
    const markdown = selected.map((item) => {
      if (!item.path) {
        return selectedItemMarkdown(item);
      }
      const sections = serverSections.get(item.path);
      const serverMarkdown = sections?.shift();
      return serverMarkdown || selectedItemMarkdown(item);
    }).filter((section) => section.trim()).join("\n\n");
    setContextMarkdown(markdown);
    await navigator.clipboard.writeText(markdown);
    setCopied(true);
    window.setTimeout(() => setCopied(false), 1200);
  }

  // @intent persist the bearer token used by browser API requests.
  function saveToken() {
    localStorage.setItem(tokenStorageKey, pendingToken);
    setToken(pendingToken);
    setNeedsToken(false);
  }

  // @intent add a file or symbol summary to the context tray without duplicates.
  function addSelected(item: SelectedDoc) {
    if (!item.path && !item.summary) return;
    setSelected((current) => current.some((doc) => doc.path === item.path && doc.label === item.label) ? current : [...current, item]);
    setContextMarkdown("");
  }

  // @intent remove one context tray item by its stable path/label pair.
  function removeSelected(path: string, label: string) {
    setSelected((current) => current.filter((item) => item.path !== path || item.label !== label));
    setContextMarkdown("");
  }

  // @intent open a force-graph node through the same document/symbol viewer used by the tree.
  function openGraphNode(node: GraphNode) {
    setViewMode("docs");
    const item: SelectedDoc = {
      path: node.doc_path || "",
      label: node.label,
      kind: normalizeKind(node.kind, `graph:${node.id}`, node.doc_path),
      summary: node.file_path || node.qualified_name,
      details: node.details || {
        qualified_name: node.qualified_name,
        file_path: node.file_path,
        start_line: node.start_line || 0,
        end_line: node.end_line || 0,
        language: node.language || ""
      }
    };
    void openDoc(item);
  }

  // @intent normalize API errors into token prompts or compact error banners.
  function handleError(err: unknown) {
    if (err instanceof APIError && err.status === 401) {
      setNeedsToken(true);
      setError("Bearer token required.");
      return;
    }
    setError(err instanceof Error ? err.message : String(err));
  }

  return (
    <div className="h-screen bg-neutral-100 text-neutral-950">
      <header className="flex min-h-14 flex-wrap items-center gap-3 border-b border-neutral-300 bg-white px-4 py-2">
        <div className="flex items-center gap-2 font-semibold">
          <FolderTree className="h-5 w-5 text-emerald-700" />
          CCG Wiki
        </div>
        <select
          className="h-9 min-w-40 rounded border border-neutral-300 bg-white px-3 text-sm"
          value={namespace}
          onChange={(event) => setNamespace(event.target.value)}
        >
          {namespaces.map((item) => <option key={item} value={item}>{item}</option>)}
        </select>
        <div className="relative min-w-60 flex-1">
          <Search className="pointer-events-none absolute left-3 top-2.5 h-4 w-4 text-neutral-500" />
          <input
            className="h-9 w-full rounded border border-neutral-300 bg-white pl-9 pr-3 text-sm outline-none focus:border-emerald-600"
            value={searchQuery}
            onChange={(event) => setSearchQuery(event.target.value)}
            placeholder={searchMode === "retrieve" ? "Retrieve docs with PageIndex" : "Search labels and summaries"}
          />
        </div>
        <div className="segmented-control">
          <button className={searchMode === "search" ? "active" : ""} onClick={() => setSearchMode("search")}>Search</button>
          <button className={searchMode === "retrieve" ? "active" : ""} onClick={() => setSearchMode("retrieve")}>Retrieve</button>
        </div>
        <div className="segmented-control">
          <button className={viewMode === "docs" ? "active" : ""} onClick={() => setViewMode("docs")}>Docs</button>
          <button className={viewMode === "graph" ? "active" : ""} onClick={() => setViewMode("graph")}>Graph</button>
        </div>
        <button className="icon-button" onClick={() => void refresh()} title="Refresh">
          {loading ? <Loader2 className="h-4 w-4 animate-spin" /> : <RefreshCw className="h-4 w-4" />}
        </button>
        <button className="command-button" onClick={() => void copyContext()} disabled={selected.length === 0}>
          {copied ? <Check className="h-4 w-4" /> : <Copy className="h-4 w-4" />}
          Copy Context
        </button>
      </header>

      {needsToken && (
        <div className="flex items-center gap-2 border-b border-amber-300 bg-amber-50 px-4 py-2 text-sm">
          <KeyRound className="h-4 w-4 text-amber-700" />
          <input
            className="h-8 w-80 rounded border border-amber-300 bg-white px-2 outline-none"
            value={pendingToken}
            onChange={(event) => setPendingToken(event.target.value)}
            placeholder="Bearer token"
            type="password"
          />
          <button className="small-button" onClick={saveToken}>Apply</button>
        </div>
      )}

      {error && (
        <div className="flex items-center gap-2 border-b border-red-200 bg-red-50 px-4 py-2 text-sm text-red-800">
          <AlertCircle className="h-4 w-4" />
          <span>{error}</span>
          <button className="ml-auto" onClick={() => setError("")}><X className="h-4 w-4" /></button>
        </div>
      )}

      <main className="grid min-h-[calc(100vh-3.5rem)] grid-cols-1 lg:h-[calc(100vh-3.5rem)] lg:grid-cols-[320px_minmax(0,1fr)_340px]">
        <aside className="overflow-auto border-r border-neutral-300 bg-neutral-50 p-3">
          {searchResults.length > 0 ? (
            <SearchList mode={searchMode} results={searchResults} onOpen={openDoc} onAdd={addSelected} />
          ) : tree ? (
            <TreeView node={tree} onOpen={openDoc} onAdd={addSelected} />
          ) : (
            <EmptyState label="No RAG tree loaded" />
          )}
        </aside>

        <section className="overflow-auto bg-white">
          {viewMode === "graph" ? (
            <Suspense fallback={<EmptyState label="Loading graph viewer..." />}>
              <GraphView namespace={namespace} token={token} onError={handleError} onOpenNode={openGraphNode} />
            </Suspense>
          ) : (
            <>
              <div className="border-b border-neutral-200 px-6 py-3">
                <div className="flex items-center gap-2 text-sm text-neutral-600">
                  <FileText className="h-4 w-4" />
                  <span className="truncate">{docPath || "Select a file or symbol"}</span>
                </div>
              </div>
              <article className="max-w-none px-8 py-6">
                {docContent ? (
                  <DocumentView content={docContent} />
                ) : docError ? (
                  <EmptyState label={docError} />
                ) : (
                  <EmptyState label="Open a tree item to view generated Markdown." />
                )}
              </article>
            </>
          )}
        </section>

        <aside className="flex min-h-0 flex-col border-l border-neutral-300 bg-neutral-50">
          <div className="border-b border-neutral-300 p-4">
            <div className="flex items-center gap-2 font-medium">
              <Braces className="h-4 w-4 text-emerald-700" />
              Context Tray
            </div>
            <div className="mt-1 text-xs text-neutral-500">~{tokenEstimate} tokens</div>
          </div>
          <div className="flex-1 overflow-auto p-3">
            {selected.length === 0 ? (
              <EmptyState label="Add files or symbols from the tree." />
            ) : selected.map((item) => (
              <div key={`${item.path}:${item.label}`} className="mb-2 rounded border border-neutral-300 bg-white p-3">
                <div className="flex items-start gap-2">
                  <NodeIcon kind={item.kind} className="mt-0.5 h-4 w-4 shrink-0 text-neutral-500" />
                  <div className="min-w-0 flex-1">
                    <div className="flex min-w-0 items-center gap-2">
                      <div className="truncate text-sm font-medium">{item.label}</div>
                      <span className="kind-chip">{item.kind}</span>
                    </div>
                    <div className="truncate text-xs text-neutral-500">{item.path || "summary only"}</div>
                  </div>
                  <button onClick={() => removeSelected(item.path, item.label)} title="Remove">
                    <X className="h-4 w-4 text-neutral-500" />
                  </button>
                </div>
              </div>
            ))}
          </div>
          <div className="border-t border-neutral-300 p-3">
            <button className="command-button w-full justify-center" onClick={() => void copyContext()} disabled={selected.length === 0}>
              {copied ? <Check className="h-4 w-4" /> : <Copy className="h-4 w-4" />}
              Copy Context
            </button>
          </div>
        </aside>
      </main>
    </div>
  );
}

// @intent render top-level RAG tree children for the active namespace.
function TreeView({ node, onOpen, onAdd }: { node: TreeNode; onOpen: (item: SelectedDoc) => void; onAdd: (item: SelectedDoc) => void }) {
  return (
    <div className="space-y-1">
      {(node.children || []).map((child) => (
        <TreeItem key={child.id} node={child} depth={0} onOpen={onOpen} onAdd={onAdd} />
      ))}
    </div>
  );
}

// @intent render one expandable RAG tree node with open and add-to-context actions.
function TreeItem({ node, depth, onOpen, onAdd }: { node: TreeNode; depth: number; onOpen: (item: SelectedDoc) => void; onAdd: (item: SelectedDoc) => void }) {
  const [open, setOpen] = useState(depth < 1);
  const children = node.children || [];
  const kind = normalizeKind(node.kind, node.id, node.doc_path);
  const item = { path: node.doc_path || "", label: node.label, kind, summary: node.summary || "", details: node.details };

  return (
    <div>
      <div className="tree-row" style={{ paddingLeft: `${depth * 14 + 4}px` }}>
        <button className="h-5 w-5" onClick={() => setOpen(!open)}>
          {children.length > 0 ? (open ? <ChevronDown className="h-4 w-4" /> : <ChevronRight className="h-4 w-4" />) : null}
        </button>
        <NodeIcon kind={kind} className="h-4 w-4 shrink-0 text-neutral-500" />
        <button className="min-w-0 flex-1 truncate text-left" onClick={() => void onOpen(item)}>{node.label}</button>
        <span className="kind-chip">{kind}</span>
        <button className="rounded px-1 text-xs text-neutral-500 hover:bg-neutral-200" onClick={() => onAdd(item)}>+</button>
      </div>
      {node.summary && <div className="truncate pb-1 pr-2 text-xs text-neutral-500" style={{ paddingLeft: `${depth * 14 + 34}px` }}>{node.summary}</div>}
      {open && children.map((child) => (
        <TreeItem key={child.id} node={child} depth={depth + 1} onOpen={onOpen} onAdd={onAdd} />
      ))}
    </div>
  );
}

// @intent show filtered search hits using the same open/add actions as the tree.
function SearchList({ mode, results, onOpen, onAdd }: { mode: SearchMode; results: SelectedDoc[]; onOpen: (item: SelectedDoc) => void; onAdd: (item: SelectedDoc) => void }) {
  return (
    <div className="space-y-2">
      {results.map((item) => (
        <div key={`${item.path || item.details?.qualified_name || item.label}:${item.label}`} className="rounded border border-neutral-300 bg-white p-3">
          <button className="flex w-full min-w-0 items-center gap-2 text-left text-sm font-medium" onClick={() => void onOpen(item)}>
            <NodeIcon kind={item.kind} className="h-4 w-4 shrink-0 text-neutral-500" />
            <span className="truncate">{item.label}</span>
            <span className="kind-chip ml-auto">{item.kind}</span>
          </button>
          {mode === "retrieve" && (
            <>
              <div className="mt-2 flex flex-wrap items-center gap-1.5">
                {typeof item.score === "number" && <span className="score-chip">score {item.score}</span>}
                {(item.matchedTerms || []).map((term) => <span key={term} className="match-chip">{term}</span>)}
              </div>
              <EvidenceList evidence={item.evidence || []} />
            </>
          )}
          <div className="mt-1 line-clamp-2 text-xs text-neutral-500">{item.summary}</div>
          <button className="small-button mt-2" onClick={() => onAdd(item)}>Add</button>
        </div>
      ))}
    </div>
  );
}

// @intent render the matched tree nodes that explain why one retrieved file ranked.
function EvidenceList({ evidence }: { evidence: RetrieveEvidence[] }) {
  if (evidence.length === 0) return null;
  const visible = evidence.slice(0, 3);
  const remaining = evidence.length - visible.length;
  return (
    <div className="retrieve-evidence">
      <div className="evidence-heading">Evidence</div>
      <div className="space-y-1">
        {visible.map((item) => (
          <div key={`${item.id}:${item.label}`} className="evidence-row">
            <NodeIcon kind={item.kind} className="mt-0.5 h-3.5 w-3.5 shrink-0 text-neutral-500" />
            <div className="min-w-0 flex-1">
              <div className="flex min-w-0 items-center gap-1.5">
                <span className="truncate font-mono text-[11px] font-semibold text-neutral-800">{item.label}</span>
                <span className="kind-chip">{item.kind}</span>
              </div>
              <div className="mt-0.5 truncate text-[11px] text-neutral-500">{item.summary || item.treePath.join(" / ")}</div>
            </div>
          </div>
        ))}
      </div>
      {remaining > 0 && <div className="mt-1 text-[11px] text-neutral-500">+{remaining} more evidence nodes</div>}
    </div>
  );
}

// @intent render compact placeholder copy for empty Wiki panels.
function EmptyState({ label }: { label: string }) {
  return <div className="py-10 text-center text-sm text-neutral-500">{label}</div>;
}

// @intent build a local fallback Markdown context when server-side docs are unavailable.
function selectedText(items: SelectedDoc[]) {
  return items.map(selectedItemMarkdown).join("\n\n");
}

// @intent render one selected file or doc-less symbol as Markdown for context copying.
function selectedItemMarkdown(item: SelectedDoc) {
  if (!item.path && item.details) {
    return symbolDetailMarkdown(item);
  }
  return `## ${item.label}\n\n${item.summary || item.path}`;
}

// @intent turn a doc-less symbol tree node into generated-doc-shaped Markdown for the visual reader.
function symbolDetailMarkdown(item: SelectedDoc) {
  const detail = item.details;
  if (!detail) {
    return `# ${item.label}\n\n${item.summary || `${item.kind} node`}`;
  }

  const annotation = detail.annotation;
  const lines: string[] = [
    "<!-- generated-by: code-context-graph docs -->",
    `# ${detail.qualified_name || item.label}`,
    "",
    `> ${detail.file_path || item.summary || `${item.kind} node`}`,
    "",
    `## ${sectionTitleForKind(item.kind)}`,
    "",
    `### ${item.label}`,
    ""
  ];

  const description = annotation?.summary || item.summary || annotation?.context || "";
  if (description) {
    lines.push(markdownLine(description), "");
  }
  if (detail.start_line > 0 || detail.end_line > 0) {
    lines.push(`- **Lines:** ${lineRange(detail.start_line, detail.end_line)}`);
  }
  if (detail.file_path) {
    lines.push(`- **File:** ${markdownLine(detail.file_path)}`);
  }
  if (detail.language) {
    lines.push(`- **Language:** ${markdownLine(detail.language)}`);
  }
  for (const block of annotationBlocks(annotation?.tags || [])) {
    if (block.inline) {
      lines.push(`- **${block.label}:** ${block.items.map(markdownLine).join(", ")}`);
      continue;
    }
    lines.push(`- **${block.label}:**`);
    for (const value of block.items) {
      lines.push(`  - ${markdownLine(value)}`);
    }
  }
  if (annotation?.context) {
    lines.push("- **Context:**", `  - ${markdownLine(annotation.context)}`);
  }
  return lines.join("\n");
}

type AnnotationBlock = {
  label: string;
  items: string[];
  inline?: boolean;
};

// @intent group raw annotation tags into the labels already supported by the generated document renderer.
function annotationBlocks(tags: NonNullable<NodeDetails["annotation"]>["tags"]) {
  const blocks: AnnotationBlock[] = [];
  const byLabel = new Map<string, AnnotationBlock>();

  function block(label: string, inline = false) {
    const key = `${label}:${inline}`;
    let existing = byLabel.get(key);
    if (!existing) {
      existing = { label, items: [], inline };
      byLabel.set(key, existing);
      blocks.push(existing);
    }
    return existing;
  }

  for (const tag of [...tags].sort((a, b) => a.ordinal - b.ordinal)) {
    const value = tagValue(tag);
    if (!value) continue;
    switch (tag.kind) {
      case "intent":
        block("Intent").items.push(value);
        break;
      case "domainRule":
        block("Domain Rules").items.push(value);
        break;
      case "sideEffect":
        block("Side Effects").items.push(value);
        break;
      case "mutates":
        block("Mutates").items.push(value);
        break;
      case "requires":
        block("Requires").items.push(value);
        break;
      case "ensures":
        block("Ensures").items.push(value);
        break;
      case "param":
        block("Params").items.push(formatParamTag(tag));
        break;
      case "return":
        block("Returns").items.push(value);
        break;
      case "see":
        block("See", true).items.push(value);
        break;
      case "throws":
        block("Throws").items.push(value);
        break;
      case "typedef":
        block("Typedef").items.push(value);
        break;
      case "index":
        block("Index").items.push(value);
        break;
      default:
        block(tag.kind || "Annotation").items.push(value);
    }
  }
  return blocks;
}

function tagValue(tag: NonNullable<NodeDetails["annotation"]>["tags"][number]) {
  return [tag.name, tag.value].filter(Boolean).join(tag.name && tag.value ? " — " : "").trim();
}

function formatParamTag(tag: NonNullable<NodeDetails["annotation"]>["tags"][number]) {
  const name = tag.name || "param";
  const typedName = tag.type ? `${name} [${tag.type}]` : name;
  return tag.value ? `${typedName} — ${tag.value}` : typedName;
}

function sectionTitleForKind(kind: string) {
  switch (kind) {
    case "class":
      return "Classes";
    case "type":
      return "Types";
    case "test":
      return "Tests";
    default:
      return "Functions";
  }
}

function lineRange(start: number, end: number) {
  if (start > 0 && end > 0) return `${start}-${end}`;
  if (start > 0) return `${start}`;
  if (end > 0) return `${end}`;
  return "";
}

function markdownLine(value: string) {
  return value.replace(/\s+/g, " ").trim();
}

// @intent compress PageIndex retrieval metadata into a scan-friendly result summary.
function retrieveSummary(summary: string, terms: string[], score: number, evidenceCount: number) {
  const parts = [];
  if (summary) parts.push(summary);
  if (terms.length > 0) parts.push(`matched: ${terms.join(", ")}`);
  parts.push(`score: ${score}`);
  if (evidenceCount > 0) parts.push(`${evidenceCount} evidence nodes`);
  return parts.join(" · ");
}

// @intent normalize Retrieve matches into compact evidence items for result cards.
function retrieveEvidence(matches: DocSearchResult[] | undefined): RetrieveEvidence[] {
  return (matches || []).map((item) => ({
    id: item.id,
    path: item.doc_path || "",
    label: item.label,
    kind: normalizeKind(item.kind, item.id, item.doc_path),
    summary: item.summary || "",
    treePath: item.path || []
  }));
}

// @intent normalize RAG tree node type metadata while remaining compatible with older doc-index files.
function normalizeKind(kind: string | undefined, id: string, docPath?: string) {
  if (kind) return kind;
  if (id.startsWith("community:")) return "community";
  if (id.startsWith("package:")) return "package";
  if (id.startsWith("symbol:")) return "symbol";
  if (id === "root") return "root";
  return docPath ? "file" : "item";
}

// @intent give Wiki tree rows a quick visual distinction between communities, packages, files, and symbols.
function NodeIcon({ kind, className }: { kind: string; className?: string }) {
  switch (kind) {
    case "community":
    case "folder":
    case "root":
      return <Folder className={className} />;
    case "package":
      return <Package className={className} />;
    case "symbol":
      return <Code2 className={className} />;
    case "file":
      return <FileCode2 className={className} />;
    default:
      return <FileText className={className} />;
  }
}

type DocAttribute = {
  label: string;
  value: string;
  items: string[];
};

type DocSymbol = {
  name: string;
  lines: string;
  description: string[];
  attributes: DocAttribute[];
};

type DocSection = {
  title: string;
  symbols: DocSymbol[];
};

type GeneratedDoc = {
  title: string;
  summary: string;
  sections: DocSection[];
};

// @intent switch generated ccg Markdown into a denser visual reader while preserving generic Markdown fallback.
function DocumentView({ content }: { content: string }) {
  const generated = parseGeneratedDoc(content);
  if (!generated) {
    return (
      <div className="markdown-body">
        <ReactMarkdown>{content}</ReactMarkdown>
      </div>
    );
  }
  return <GeneratedDocView doc={generated} />;
}

// @intent render generated file docs as browsable symbol cards instead of a flat Markdown page.
function GeneratedDocView({ doc }: { doc: GeneratedDoc }) {
  const totalSymbols = doc.sections.reduce((sum, section) => sum + section.symbols.length, 0);
  return (
    <div className="space-y-6">
      <div className="doc-hero">
        <div className="flex min-w-0 items-start gap-3">
          <div className="rounded bg-emerald-700 p-2 text-white">
            <FileText className="h-5 w-5" />
          </div>
          <div className="min-w-0">
            <div className="break-all font-mono text-xl font-semibold text-neutral-950">{doc.title}</div>
            {doc.summary && <p className="mt-2 max-w-3xl text-sm leading-6 text-neutral-700">{doc.summary}</p>}
          </div>
        </div>
        <div className="mt-4 flex flex-wrap gap-2">
          <span className="doc-stat">{totalSymbols} symbols</span>
          {doc.sections.map((section) => (
            <span key={section.title} className="doc-stat">{section.symbols.length} {section.title}</span>
          ))}
        </div>
      </div>

      {doc.sections.map((section) => (
        <section key={section.title} className="space-y-3">
          <div className="doc-section-title">
            <Braces className="h-4 w-4 text-emerald-700" />
            <h2>{section.title}</h2>
            <span>{section.symbols.length}</span>
          </div>
          <div className="grid gap-3 xl:grid-cols-2">
            {section.symbols.map((symbol) => <SymbolCard key={`${section.title}:${symbol.name}`} symbol={symbol} />)}
          </div>
        </section>
      ))}
    </div>
  );
}

// @intent show one generated function/type entry with metadata grouped by annotation kind.
function SymbolCard({ symbol }: { symbol: DocSymbol }) {
  const visibleAttributes = symbol.attributes.filter((attribute) => attribute.label !== "Lines");
  return (
    <div className="symbol-card">
      <div className="flex items-start justify-between gap-3">
        <div className="min-w-0">
          <h3 className="break-words font-mono text-base font-semibold text-neutral-950">{symbol.name}</h3>
          {symbol.description.length > 0 && (
            <p className="mt-2 text-sm leading-6 text-neutral-700">{symbol.description.join(" ")}</p>
          )}
        </div>
        {symbol.lines && <span className="line-badge">Lines {symbol.lines}</span>}
      </div>

      {visibleAttributes.length > 0 && (
        <div className="mt-4 space-y-3">
          {visibleAttributes.map((attribute) => <AttributeBlock key={attribute.label} attribute={attribute} />)}
        </div>
      )}
    </div>
  );
}

// @intent format generated annotation fields as readable chips and short labeled blocks.
function AttributeBlock({ attribute }: { attribute: DocAttribute }) {
  const label = attribute.label;
  if ((label === "Calls" || label === "See") && attribute.value) {
    return (
      <div>
        <div className="attribute-label">{label}</div>
        <div className="mt-1 flex flex-wrap gap-1.5">
          {attribute.value.split(",").map((item) => item.trim()).filter(Boolean).map((item) => (
            <span key={item} className={label === "Calls" ? "call-chip" : "see-chip"}>{item}</span>
          ))}
        </div>
      </div>
    );
  }

  if (label === "Params" && attribute.items.length > 0) {
    return (
      <div>
        <div className="attribute-label">{label}</div>
        <ul className="mt-1 space-y-1">
          {attribute.items.map((item) => {
            const [name, detail] = splitParam(item);
            return (
              <li key={item} className="param-row">
                <span>{name}</span>
                <p>{detail}</p>
              </li>
            );
          })}
        </ul>
      </div>
    );
  }

  if (blockClass(label)) {
    return (
      <div className={blockClass(label)}>
        <div className="attribute-label">{label}</div>
        {attribute.items.length > 0 ? (
          <ul className="mt-2 space-y-1.5">
            {attribute.items.map((item) => <li key={item} className="annotation-list-item">{item}</li>)}
          </ul>
        ) : attribute.value ? (
          <div className="mt-1 text-sm leading-6 text-neutral-800">{attribute.value}</div>
        ) : null}
      </div>
    );
  }

  if (attribute.items.length > 0) {
    return (
      <div>
        <div className="attribute-label">{label}</div>
        <ul className="mt-1 space-y-1">
          {attribute.items.map((item) => <li key={item} className="attribute-item">{item}</li>)}
        </ul>
      </div>
    );
  }

  return (
    <div>
      <div className="attribute-label">{label}</div>
      {attribute.value && <div className="mt-1 text-sm leading-6 text-neutral-700">{attribute.value}</div>}
    </div>
  );
}

function blockClass(label: string) {
  switch (label) {
    case "Intent":
      return "intent-block";
    case "Domain Rules":
      return "domain-block";
    case "Side Effects":
      return "side-effect-block";
    case "Mutates":
      return "mutates-block";
    case "Requires":
      return "requires-block";
    case "Ensures":
      return "ensures-block";
    case "Returns":
      return "returns-block";
    default:
      return "";
  }
}

function splitParam(item: string) {
  const parts = item.split(/\s+—\s+/);
  if (parts.length >= 2) {
    return [parts[0], parts.slice(1).join(" — ")];
  }
  return [item, ""];
}

// @intent parse the stable ccg generated Markdown shape into title, section, symbol, and annotation records.
function parseGeneratedDoc(content: string): GeneratedDoc | null {
  if (!content.includes("generated-by: code-context-graph docs")) return null;

  const lines = content.split(/\r?\n/);
  const titleLine = lines.find((line) => line.startsWith("# "));
  if (!titleLine) return null;

  const doc: GeneratedDoc = {
    title: titleLine.replace(/^#\s+/, "").trim(),
    summary: "",
    sections: []
  };
  let currentSection: DocSection | null = null;
  let currentSymbol: DocSymbol | null = null;
  let currentAttribute: DocAttribute | null = null;

  for (const rawLine of lines) {
    const line = rawLine.trimEnd();
    const trimmed = line.trim();
    if (!trimmed || trimmed.startsWith("<!--") || trimmed === titleLine) continue;

    if (trimmed.startsWith(">")) {
      doc.summary = appendSentence(doc.summary, cleanInline(trimmed.replace(/^>\s?/, "")));
      continue;
    }

    if (trimmed.startsWith("## ")) {
      currentSection = { title: trimmed.replace(/^##\s+/, "").trim(), symbols: [] };
      doc.sections.push(currentSection);
      currentSymbol = null;
      currentAttribute = null;
      continue;
    }

    if (trimmed.startsWith("### ")) {
      if (!currentSection) continue;
      currentSymbol = { name: trimmed.replace(/^###\s+/, "").trim(), lines: "", description: [], attributes: [] };
      currentSection.symbols.push(currentSymbol);
      currentAttribute = null;
      continue;
    }

    if (!currentSymbol) continue;

    const attributeMatch = trimmed.match(/^- \*\*([^:]+):\*\*(?:\s*(.*))?$/);
    if (attributeMatch) {
      currentAttribute = { label: attributeMatch[1], value: cleanInline(attributeMatch[2] || ""), items: [] };
      if (currentAttribute.label === "Lines") {
        currentSymbol.lines = currentAttribute.value;
      }
      currentSymbol.attributes.push(currentAttribute);
      continue;
    }

    const itemMatch = trimmed.match(/^- (.+)$/);
    if (itemMatch && currentAttribute) {
      currentAttribute.items.push(cleanInline(itemMatch[1]));
      continue;
    }

    currentSymbol.description.push(cleanInline(trimmed));
  }

  doc.sections = doc.sections.filter((section) => section.symbols.length > 0);
  return doc.sections.length > 0 ? doc : null;
}

// @intent keep Markdown emphasis markers out of visual metadata labels without changing source content.
function cleanInline(value: string) {
  return value
    .replace(/`([^`]+)`/g, "$1")
    .replace(/\*\*([^*]+)\*\*/g, "$1")
    .trim();
}

function appendSentence(current: string, next: string) {
  if (!next) return current;
  return current ? `${current} ${next}` : next;
}
