import { useEffect, useMemo, useRef, useState } from "react";
import { forceCollide } from "d3-force";
import ForceGraph2D, { ForceGraphMethods, LinkObject, NodeObject } from "react-force-graph-2d";
import { AlertCircle, Maximize2, RefreshCw, Search, Share2 } from "lucide-react";
import { APIError, GraphEdge, GraphNode, getGraph } from "./api";

// @intent configure the namespace graph viewer and its node-open callback.
type GraphViewProps = {
  namespace: string;
  token: string;
  onError: (err: unknown) => void;
  onOpenNode: (node: GraphNode) => void;
};

// @intent extend graph API nodes with a numeric value used by the force layout.
type CanvasNode = GraphNode & {
  val: number;
};

// @intent allow force-graph to replace link endpoints with resolved node objects after simulation starts.
type CanvasLink = GraphEdge & {
  source: string | CanvasNode;
  target: string | CanvasNode;
};

const symbolKinds = new Set(["function", "class", "type", "test"]);
const structuralEdgeKinds = new Set(["contains", "depends_on", "references"]);
const callEdgeKinds = new Set(["calls", "fallback_calls"]);
const importEdgeKinds = new Set(["imports_from"]);
const typeEdgeKinds = new Set(["inherits", "implements", "tested_by"]);

// @intent render an Obsidian-style force-directed graph for one CCG namespace.
export function GraphView({ namespace, token, onError, onOpenNode }: GraphViewProps) {
  const graphRef = useRef<ForceGraphMethods<CanvasNode, CanvasLink> | undefined>(undefined);
  const frameRef = useRef<HTMLDivElement | null>(null);
  const [graph, setGraph] = useState<{ nodes: CanvasNode[]; links: CanvasLink[] }>({ nodes: [], links: [] });
  const [truncated, setTruncated] = useState(false);
  const [loading, setLoading] = useState(false);
  const [filter, setFilter] = useState("");
  const [showSymbols, setShowSymbols] = useState(true);
  const [showStructure, setShowStructure] = useState(true);
  const [showCalls, setShowCalls] = useState(true);
  const [showImports, setShowImports] = useState(true);
  const [showTypes, setShowTypes] = useState(true);
  const [size, setSize] = useState({ width: 960, height: 640 });

  useEffect(() => {
    const el = frameRef.current;
    if (!el) return;
    // @intent keep the canvas dimensions synchronized with the available center panel space.
    const resize = () => {
      const rect = el.getBoundingClientRect();
      setSize({ width: Math.max(320, Math.floor(rect.width)), height: Math.max(420, Math.floor(rect.height)) });
    };
    resize();
    const observer = new ResizeObserver(resize);
    observer.observe(el);
    return () => observer.disconnect();
  }, []);

  useEffect(() => {
    void load();
  }, [namespace, token]);

  const visibleGraph = useMemo(() => {
    const q = filter.trim().toLowerCase();
    const nodes = graph.nodes.filter((node) => {
      if (!showSymbols && symbolKinds.has(node.kind)) return false;
      if (!q) return true;
      return [
        node.label,
        node.kind,
        node.qualified_name,
        node.file_path,
        node.language || ""
      ].some((value) => value.toLowerCase().includes(q));
    });
    const visibleIDs = new Set(nodes.map((node) => node.id));
    const links = graph.links.filter((link) => {
      if (!edgeKindVisible(link.kind)) return false;
      const sourceID = nodeID(link.source);
      const targetID = nodeID(link.target);
      return visibleIDs.has(sourceID) && visibleIDs.has(targetID);
    });
    return { nodes, links };
  }, [filter, graph, showSymbols, showStructure, showCalls, showImports, showTypes]);

  useEffect(() => {
    configureForces();
  }, [visibleGraph.nodes.length, visibleGraph.links.length, showSymbols, showStructure, showCalls, showImports, showTypes]);

  // @intent refresh graph data when namespace or token changes.
  async function load() {
    setLoading(true);
    try {
      const res = await getGraph(namespace, token);
      setGraph({
        nodes: res.nodes.map((node) => ({ ...node, val: nodeValue(node.kind) })),
        links: res.edges.map((edge) => ({ ...edge }))
      });
      setTruncated(res.truncated);
      window.setTimeout(() => {
        configureForces();
        graphRef.current?.zoomToFit(500, 90);
      }, 120);
    } catch (err) {
      if (err instanceof APIError && err.status === 404) {
        setGraph({ nodes: [], links: [] });
        setTruncated(false);
        return;
      }
      onError(err);
    } finally {
      setLoading(false);
    }
  }

  // @intent decide whether an edge kind should be visible under the active graph filters.
  function edgeKindVisible(kind: string) {
    if (structuralEdgeKinds.has(kind)) return showStructure;
    if (callEdgeKinds.has(kind)) return showCalls;
    if (importEdgeKinds.has(kind)) return showImports;
    if (typeEdgeKinds.has(kind)) return showTypes;
    return true;
  }

  // @intent spread dense CCG graphs enough that zooming creates readable separation between nodes.
  function configureForces() {
    const graph = graphRef.current;
    if (!graph) return;
    const linkForce = graph.d3Force("link") as { distance?: (value: number | ((link: CanvasLink) => number)) => unknown; strength?: (value: number) => unknown } | undefined;
    linkForce?.distance?.((link: CanvasLink) => linkDistance(link.kind));
    linkForce?.strength?.(0.42);

    const chargeForce = graph.d3Force("charge") as { strength?: (value: number) => unknown; distanceMax?: (value: number) => unknown } | undefined;
    chargeForce?.strength?.(-115);
    chargeForce?.distanceMax?.(520);

    graph.d3Force("collide", forceCollide<NodeObject<CanvasNode>>((node) => collisionRadius(node.kind)).strength(0.74).iterations(2));
    graph.d3ReheatSimulation();
  }

  return (
    <div className="flex h-full min-h-[560px] flex-col bg-neutral-950 text-neutral-100">
      <div className="flex flex-wrap items-center gap-2 border-b border-neutral-800 bg-neutral-900 px-4 py-3">
        <div className="flex items-center gap-2 text-sm font-semibold">
          <Share2 className="h-4 w-4 text-emerald-400" />
          Graph
        </div>
        <div className="relative min-w-52 flex-1">
          <Search className="pointer-events-none absolute left-3 top-2.5 h-4 w-4 text-neutral-500" />
          <input
            className="h-9 w-full rounded border border-neutral-700 bg-neutral-950 pl-9 pr-3 text-sm text-neutral-100 outline-none focus:border-emerald-500"
            value={filter}
            onChange={(event) => setFilter(event.target.value)}
            placeholder="Filter nodes"
          />
        </div>
        <GraphToggle label="Symbols" active={showSymbols} onClick={() => setShowSymbols(!showSymbols)} />
        <GraphToggle label="Structure" active={showStructure} onClick={() => setShowStructure(!showStructure)} />
        <GraphToggle label="Calls" active={showCalls} onClick={() => setShowCalls(!showCalls)} />
        <GraphToggle label="Imports" active={showImports} onClick={() => setShowImports(!showImports)} />
        <GraphToggle label="Types" active={showTypes} onClick={() => setShowTypes(!showTypes)} />
        <button className="graph-icon-button" onClick={() => graphRef.current?.zoomToFit(500, 70)} title="Fit graph">
          <Maximize2 className="h-4 w-4" />
        </button>
        <button className="graph-icon-button" onClick={() => void load()} title="Reload graph">
          <RefreshCw className={`h-4 w-4 ${loading ? "animate-spin" : ""}`} />
        </button>
      </div>

      {truncated && (
        <div className="flex items-center gap-2 border-b border-amber-700/50 bg-amber-950/50 px-4 py-2 text-xs text-amber-100">
          <AlertCircle className="h-4 w-4 text-amber-300" />
          Graph is truncated by the API limit. Use filters to inspect a smaller region.
        </div>
      )}

      <div ref={frameRef} className="relative min-h-0 flex-1">
        {visibleGraph.nodes.length === 0 ? (
          <div className="flex h-full items-center justify-center text-sm text-neutral-500">No graph nodes match the current filters.</div>
        ) : (
          <ForceGraph2D<CanvasNode, CanvasLink>
            ref={graphRef}
            graphData={visibleGraph}
            width={size.width}
            height={size.height}
            backgroundColor="#0a0a0a"
            nodeRelSize={5}
            nodeVal={(node) => node.val}
            nodeLabel={(node) => nodeTitle(node)}
            nodeColor={(node) => nodeColor(node.kind)}
            nodeCanvasObjectMode={() => "replace"}
            nodeCanvasObject={paintNode}
            linkColor={(link) => edgeColor(link.kind)}
            linkWidth={(link) => edgeWidth(link.kind)}
            linkDirectionalArrowLength={3}
            linkDirectionalArrowRelPos={0.92}
            linkDirectionalParticles={(link) => callEdgeKinds.has(link.kind) ? 1 : 0}
            linkDirectionalParticleWidth={1.4}
            linkLabel={(link) => `${link.kind}${link.file_path ? ` · ${link.file_path}` : ""}`}
            cooldownTicks={90}
            d3VelocityDecay={0.32}
            onNodeClick={(node) => onOpenNode(node)}
          />
        )}
        <div className="graph-legend">
          <LegendDot label="package" color="#10b981" />
          <LegendDot label="file" color="#38bdf8" />
          <LegendDot label="symbol" color="#a78bfa" />
          <LegendDot label="test" color="#f59e0b" />
        </div>
      </div>
    </div>
  );
}

// @intent render one compact graph filter toggle.
function GraphToggle({ label, active, onClick }: { label: string; active: boolean; onClick: () => void }) {
  return (
    <button className={active ? "graph-toggle active" : "graph-toggle"} onClick={onClick}>
      {label}
    </button>
  );
}

// @intent render a legend item that matches graph node colors.
function LegendDot({ label, color }: { label: string; color: string }) {
  return (
    <div className="flex items-center gap-1.5">
      <span className="h-2.5 w-2.5 rounded-full" style={{ backgroundColor: color }} />
      <span>{label}</span>
    </div>
  );
}

// @intent paint graph nodes with readable labels at normal zoom levels.
function paintNode(node: NodeObject<CanvasNode>, ctx: CanvasRenderingContext2D, globalScale: number) {
  const radius = screenRadiusForKind(node.kind) / globalScale;
  ctx.beginPath();
  ctx.arc(node.x || 0, node.y || 0, radius, 0, 2 * Math.PI, false);
  ctx.fillStyle = nodeColor(node.kind);
  ctx.shadowBlur = 12;
  ctx.shadowColor = nodeColor(node.kind);
  ctx.fill();
  ctx.shadowBlur = 0;
  ctx.lineWidth = 1.2 / globalScale;
  ctx.strokeStyle = "rgba(255,255,255,0.68)";
  ctx.stroke();

  if (globalScale < 0.55 && !["file", "package"].includes(node.kind)) return;
  const label = compactLabel(node.label || node.qualified_name || node.file_path);
  const fontSize = 11 / globalScale;
  ctx.font = `${fontSize}px ui-monospace, SFMono-Regular, Menlo, Monaco, Consolas, monospace`;
  ctx.textAlign = "center";
  ctx.textBaseline = "top";
  ctx.fillStyle = "rgba(245,245,245,0.92)";
  ctx.fillText(label, node.x || 0, (node.y || 0) + radius + 3 / globalScale);
}

function nodeTitle(node: NodeObject<CanvasNode>) {
  return [node.qualified_name || node.label, node.kind, node.file_path].filter(Boolean).join(" · ");
}

function nodeID(node: string | CanvasNode | NodeObject<CanvasNode> | undefined) {
  if (!node) return "";
  if (typeof node === "string") return node;
  return String(node.id || "");
}

function compactLabel(label: string) {
  if (label.length <= 32) return label;
  return `${label.slice(0, 29)}...`;
}

function nodeValue(kind: string) {
  switch (kind) {
    case "package":
      return 8;
    case "file":
      return 6;
    case "class":
    case "type":
      return 5;
    case "test":
      return 4.5;
    default:
      return 4;
  }
}

function nodeColor(kind: string) {
  switch (kind) {
    case "package":
      return "#10b981";
    case "file":
      return "#38bdf8";
    case "class":
    case "type":
      return "#a78bfa";
    case "test":
      return "#f59e0b";
    default:
      return "#d4d4d4";
  }
}

function edgeColor(kind: string) {
  if (callEdgeKinds.has(kind)) return "rgba(52, 211, 153, 0.72)";
  if (importEdgeKinds.has(kind)) return "rgba(56, 189, 248, 0.58)";
  if (typeEdgeKinds.has(kind)) return "rgba(167, 139, 250, 0.62)";
  return "rgba(212, 212, 212, 0.22)";
}

function edgeWidth(kind: string) {
  if (callEdgeKinds.has(kind)) return 1.4;
  if (importEdgeKinds.has(kind) || typeEdgeKinds.has(kind)) return 1.1;
  return 0.7;
}

// @intent separate semantic edges enough for zoomed graph inspection without losing structural context.
function linkDistance(kind: string) {
  if (callEdgeKinds.has(kind)) return 110;
  if (importEdgeKinds.has(kind) || typeEdgeKinds.has(kind)) return 92;
  return 68;
}

// @intent reserve layout space around each node so dense graph clusters do not collapse.
function collisionRadius(kind: string) {
  switch (kind) {
    case "package":
      return 24;
    case "file":
      return 20;
    case "class":
    case "type":
      return 18;
    default:
      return 15;
  }
}

// @intent keep node circles a stable screen size while zooming changes node separation.
function screenRadiusForKind(kind: string) {
  switch (kind) {
    case "package":
      return 8.5;
    case "file":
      return 7.5;
    case "class":
    case "type":
      return 7;
    case "test":
      return 6.5;
    default:
      return 6;
  }
}
