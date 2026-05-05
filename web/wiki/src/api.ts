// @intent describe one node in the Wiki RAG tree returned by ccg-server.
export type TreeNode = {
  id: string;
  label: string;
  kind?: string;
  summary: string;
  doc_path?: string;
  details?: NodeDetails;
  children?: TreeNode[] | null;
};

// @intent expose structured symbol metadata stored in wiki-index.json.
export type NodeDetails = {
  qualified_name: string;
  file_path: string;
  start_line: number;
  end_line: number;
  language: string;
  annotation?: AnnotationDetails;
};

// @intent carry annotation summary and tags for symbol detail rendering.
export type AnnotationDetails = {
  summary: string;
  context: string;
  tags: AnnotationTag[];
};

// @intent mirror one CCG annotation tag in the browser API type system.
export type AnnotationTag = {
  kind: string;
  type: string;
  name: string;
  value: string;
  ordinal: number;
  ref?: CCGRef;
};

// @intent describe a parsed ccg:// cross-namespace reference attached to @see annotations.
export type CCGRef = {
  raw: string;
  namespace: string;
  path?: string;
  symbol?: string;
  scope: string;
};

// @intent carry a namespace-scoped RAG tree payload from the Wiki API.
export type TreeResponse = {
  namespace: string;
  built_at: string;
  root: TreeNode;
};

// @intent represent a tree search hit that can be opened or added to LLM context.
export type SearchResult = {
  id: string;
  label: string;
  kind?: string;
  summary: string;
  doc_path?: string;
  details?: NodeDetails;
  path: string[];
};

// @intent represent one PageIndex retrieval result with evidence from doc-index.json.
export type RetrieveResult = {
  id: string;
  label: string;
  kind?: string;
  summary: string;
  doc_path: string;
  details?: NodeDetails;
  path: string[];
  score: number;
  matched_terms: string[];
  matches?: SearchResult[];
  content?: string;
  content_truncated?: boolean;
};

// @intent describe one graph database node exposed to the Wiki graph viewer.
export type GraphNode = {
  id: string;
  label: string;
  kind: string;
  qualified_name: string;
  file_path: string;
  doc_path?: string;
  start_line?: number;
  end_line?: number;
  language?: string;
  details?: NodeDetails;
};

// @intent describe one graph database edge exposed to the Wiki graph viewer.
export type GraphEdge = {
  id: string;
  source: string;
  target: string;
  kind: string;
  file_path?: string;
  line?: number;
};

// @intent carry bounded namespace graph data for the visual graph tab.
export type GraphResponse = {
  namespace: string;
  limit: number;
  truncated: boolean;
  nodes: GraphNode[];
  edges: GraphEdge[];
};

// @intent return generated Markdown content for one documentation path.
export type DocResponse = {
  namespace: string;
  path: string;
  resolved: string;
  content: string;
};

// @intent return a server-assembled Markdown bundle for selected docs.
export type ContextResponse = {
  markdown: string;
  items: Array<{ path: string; label?: string; found: boolean; markdown?: string; error?: string }>;
};

const apiBase = "/wiki/api";

// @intent preserve HTTP status alongside user-facing Wiki API errors.
export class APIError extends Error {
  status: number;

  // @intent attach the HTTP status to a normal Error instance.
  constructor(status: number, message: string) {
    super(message);
    this.status = status;
  }
}

// @intent apply bearer auth and consistent JSON error handling to Wiki API calls.
async function request<T>(path: string, token: string, init: RequestInit = {}): Promise<T> {
  const headers = new Headers(init.headers);
  headers.set("Accept", "application/json");
  if (init.body && !headers.has("Content-Type")) {
    headers.set("Content-Type", "application/json");
  }
  if (token.trim()) {
    headers.set("Authorization", `Bearer ${token.trim()}`);
  }
  const res = await fetch(`${apiBase}${path}`, { ...init, headers });
  if (!res.ok) {
    let message = `${res.status} ${res.statusText}`;
    const textFallback = res.clone();
    try {
      const body = await res.json();
      message = body.detail || body.error || message;
    } catch {
      message = await textFallback.text();
    }
    throw new APIError(res.status, message);
  }
  return res.json() as Promise<T>;
}

// @intent load namespaces available to the Wiki selector.
export function listNamespaces(token: string) {
  return request<{ namespaces: string[] }>("/namespaces", token);
}

// @intent load the RAG tree for the active namespace.
export function getTree(namespace: string, token: string) {
  return request<TreeResponse>(`/tree?namespace=${encodeURIComponent(namespace)}`, token);
}

// @intent load generated Markdown for the selected tree item.
export function getDoc(namespace: string, path: string, token: string) {
  const qs = new URLSearchParams({ namespace, path });
  return request<DocResponse>(`/doc?${qs.toString()}`, token);
}

// @intent search the active namespace's RAG tree by label and summary.
export function searchDocs(namespace: string, query: string, token: string) {
  const qs = new URLSearchParams({ namespace, q: query, limit: "25" });
  return request<{ namespace: string; results: SearchResult[] }>(`/search?${qs.toString()}`, token);
}

// @intent retrieve ranked generated docs using the PageIndex doc-index tree.
export function retrieveDocs(namespace: string, query: string, token: string) {
  const qs = new URLSearchParams({ namespace, q: query, limit: "10", content_limit: "0" });
  return request<{ namespace: string; results: RetrieveResult[] }>(`/retrieve?${qs.toString()}`, token);
}

// @intent load a bounded namespace graph for the visual graph tab.
export function getGraph(namespace: string, token: string) {
  const qs = new URLSearchParams({ namespace, limit: "1200" });
  return request<GraphResponse>(`/graph?${qs.toString()}`, token);
}

// @intent ask ccg-server to assemble selected docs into one LLM-ready Markdown block.
export function buildContext(namespace: string, paths: string[], token: string) {
  return request<ContextResponse>("/context", token, {
    method: "POST",
    body: JSON.stringify({ namespace, paths })
  });
}
