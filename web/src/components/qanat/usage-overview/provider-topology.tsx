/*
 * Ported from 9router (https://github.com/decolua/9router),
 * src/app/(dashboard)/dashboard/usage/components/ProviderTopology.js.
 * MIT License, Copyright (c) 2024-2026 decolua and contributors. See LICENSE-9router.
 * Qanat changes: the center node is Qanat; provider nodes use the theme's
 * colors; animations honor reduced motion. Provider logos (logos/*.png) are
 * 9router's public/providers icons, imported so Next bundles them.
 */
"use client";

import "@xyflow/react/dist/style.css";
import "./topology.css";

import {
  BaseEdge,
  Controls,
  getBezierPath,
  Handle,
  Position,
  ReactFlow,
  type Edge,
  type EdgeProps,
  type Node,
  type NodeProps,
  type ReactFlowInstance,
} from "@xyflow/react";
import Image, { type StaticImageData } from "next/image";
import { useCallback, useEffect, useMemo, useRef } from "react";

import { QanatMark } from "@/components/qanat/qanat-mark";
import { cn } from "@/lib/utils";

import antigravityLogo from "./logos/antigravity.png";
import claudeLogo from "./logos/claude.png";
import codexLogo from "./logos/codex.png";
import { providerName } from "./shared";

// Kame + electric particles along active edges
const KAME_PARTICLE_COUNT = 6;
const SPARK_COUNT = 5;

const PROVIDER_LOGO: Record<string, StaticImageData> = { claude: claudeLogo, codex: codexLogo, antigravity: antigravityLogo };

type ProviderData = { label: string; logo?: StaticImageData; textIcon: string; active: boolean };
type RouterData = { activeCount: number };
type EdgeData = { active: boolean };

const hidden = "!h-0 !w-0 !border-0 !bg-transparent";

// Custom provider node - rectangle with logo + name
function ProviderNode({ data }: NodeProps<Node<ProviderData>>) {
  const { label, logo, textIcon, active } = data;
  return (
    <div
      className="flex items-center gap-2.5 rounded-lg border-2 bg-background px-4 py-2.5 transition-all duration-300"
      style={{
        borderColor: active ? "var(--primary)" : "var(--border)",
        boxShadow: active ? "0 0 16px color-mix(in srgb, var(--primary) 25%, transparent)" : "none",
        minWidth: "150px",
      }}
    >
      <Handle type="target" position={Position.Top} id="top" className={hidden} />
      <Handle type="target" position={Position.Bottom} id="bottom" className={hidden} />
      <Handle type="target" position={Position.Left} id="left" className={hidden} />
      <Handle type="target" position={Position.Right} id="right" className={hidden} />

      <div className="flex size-8 shrink-0 items-center justify-center rounded-md bg-brand-soft">
        {logo ? (
          <Image src={logo} alt="" width={24} height={24} className="size-6 rounded-sm object-contain" />
        ) : (
          <span className="text-sm font-bold text-brand-text">{textIcon}</span>
        )}
      </div>
      <span className={cn("truncate text-base font-medium", active && "text-primary")}>{label}</span>
      {active && (
        <span className="relative flex size-2 shrink-0">
          <span className="absolute inline-flex size-full animate-ping rounded-full bg-primary opacity-75 motion-reduce:animate-none" />
          <span className="relative inline-flex size-2 rounded-full bg-primary" />
        </span>
      )}
    </div>
  );
}

// Center Qanat node — pulse/glow on card only (no expanding rings)
function RouterNode({ data }: NodeProps<Node<RouterData>>) {
  const powering = (data.activeCount || 0) > 0;
  return (
    <div
      className={cn(
        "relative z-[1] flex min-w-[130px] items-center justify-center rounded-xl border-2 px-5 py-3",
        powering ? "topology-router-core bg-sand-soft" : "border-primary bg-brand-soft shadow-md",
      )}
    >
      <Handle type="source" position={Position.Top} id="top" className={hidden} />
      <Handle type="source" position={Position.Bottom} id="bottom" className={hidden} />
      <Handle type="source" position={Position.Left} id="left" className={hidden} />
      <Handle type="source" position={Position.Right} id="right" className={hidden} />

      <QanatMark className={cn("me-2 size-6 text-brand-text", powering && "topology-router-icon")} title="" />
      <span className={cn("text-sm font-bold text-brand-text", powering && "topology-router-label")}>Qanat</span>
      {powering && (
        <span className="topology-router-badge ms-2 rounded-full bg-sand px-1.5 py-0.5 text-xs font-bold text-sand-foreground">
          {data.activeCount}
        </span>
      )}
    </div>
  );
}

// Active: electric kame beam (multi-layer stroke + sparks). Idle/last/error: solid BaseEdge.
function TopologyEdge({ id, sourceX, sourceY, targetX, targetY, sourcePosition, targetPosition, style = {}, data }: EdgeProps<Edge<EdgeData>>) {
  const [edgePath] = getBezierPath({ sourceX, sourceY, sourcePosition, targetX, targetY, targetPosition });
  const active = !!data?.active;
  const filterId = `topo-electric-${id}`;

  if (!active) {
    return <BaseEdge id={id} path={edgePath} style={style} />;
  }

  return (
    <g className="topology-edge-electric">
      <defs>
        <filter id={filterId} x="-40%" y="-40%" width="180%" height="180%">
          <feTurbulence type="fractalNoise" baseFrequency="0.9" numOctaves={2} seed={2} result="noise">
            <animate attributeName="baseFrequency" values="0.8;1.4;0.8" dur="0.25s" repeatCount="indefinite" />
          </feTurbulence>
          <feDisplacementMap in="SourceGraphic" in2="noise" scale={3.5} xChannelSelector="R" yChannelSelector="G" />
        </filter>
      </defs>
      {/* Outer halo */}
      <path d={edgePath} fill="none" stroke="var(--primary)" strokeWidth={10} strokeOpacity={0.3} strokeLinecap="round" filter={`url(#${filterId})`} className="topology-edge-halo" />
      {/* Mid plasma */}
      <path d={edgePath} fill="none" stroke="var(--sand)" strokeWidth={5} strokeOpacity={0.85} strokeLinecap="round" filter={`url(#${filterId})`} className="topology-edge-plasma" />
      {/* Hot core */}
      <BaseEdge id={id} path={edgePath} style={{ stroke: "var(--foreground)", strokeWidth: 2.2, opacity: 1 }} className="topology-edge-kame" />
      {/* Energy orbs */}
      {Array.from({ length: KAME_PARTICLE_COUNT }, (_, i) => (
        <circle
          key={`${id}-p-${i}`}
          r={i % 2 === 0 ? 4 : 2.5}
          fill={i % 3 === 0 ? "var(--sand)" : i % 3 === 1 ? "var(--primary)" : "var(--background)"}
          opacity={0.95}
          className="motion-reduce:hidden"
        >
          <animateMotion dur={`${0.4 + i * 0.08}s`} repeatCount="indefinite" path={edgePath} begin={`${i * 0.09}s`} />
        </circle>
      ))}
      {/* Sparks (short-lived blink along path) */}
      {Array.from({ length: SPARK_COUNT }, (_, i) => (
        <circle key={`${id}-s-${i}`} r={1.8} fill="var(--sand)" opacity={0} className="motion-reduce:hidden">
          <animate attributeName="opacity" values="0;1;0;0;1;0" dur={`${0.35 + (i % 3) * 0.1}s`} begin={`${i * 0.07}s`} repeatCount="indefinite" />
          <animateMotion dur={`${0.28 + i * 0.05}s`} repeatCount="indefinite" path={edgePath} begin={`${i * 0.11}s`} />
        </circle>
      ))}
    </g>
  );
}

const nodeTypes = { provider: ProviderNode, router: RouterNode };
const edgeTypes = { topology: TopologyEdge };

// Place N nodes evenly along an ellipse around the router center.
function buildLayout(providers: string[], activeSet: Set<string>, lastSet: Set<string>, errorSet: Set<string>, activeCount: number) {
  const nodeW = 180;
  const nodeH = 30;
  const routerW = 120;
  const routerH = 44;
  const nodeGap = 24;
  const count = providers.length;

  // Compute rx so arc spacing between nodes >= nodeW + nodeGap
  const minRx = ((nodeW + nodeGap) * count) / (2 * Math.PI);
  const rx = Math.max(320, minRx);
  const ry = Math.max(200, rx * 0.55); // ellipse ratio ~0.55

  const nodes: Node[] = [
    { id: "router", type: "router", position: { x: -routerW / 2, y: -routerH / 2 }, data: { activeCount }, draggable: false },
  ];
  const edges: Edge[] = [];

  const edgeStyle = (active: boolean, last: boolean, error: boolean) => {
    if (error) return { stroke: "var(--destructive)", strokeWidth: 2.5, opacity: 0.9 };
    if (active) return { stroke: "var(--primary)", strokeWidth: 3.5, opacity: 1 };
    if (last) return { stroke: "var(--sand)", strokeWidth: 2, opacity: 0.9 };
    return { stroke: "var(--border)", strokeWidth: 1, opacity: 0.6 };
  };

  providers.forEach((p, i) => {
    const active = activeSet.has(p);
    const last = !active && lastSet.has(p);
    const error = !active && errorSet.has(p);
    const nodeId = `provider-${p}`;

    // Distribute evenly starting from top (−π/2), clockwise
    const angle = -Math.PI / 2 + (2 * Math.PI * i) / count;
    const cx = rx * Math.cos(angle);
    const cy = ry * Math.sin(angle);

    // Pick router handle closest to the node direction
    let sourceHandle: string, targetHandle: string;
    if (Math.abs(angle + Math.PI / 2) < Math.PI / 4 || Math.abs(angle - (3 * Math.PI) / 2) < Math.PI / 4) {
      sourceHandle = "top";
      targetHandle = "bottom";
    } else if (Math.abs(angle - Math.PI / 2) < Math.PI / 4) {
      sourceHandle = "bottom";
      targetHandle = "top";
    } else if (cx > 0) {
      sourceHandle = "right";
      targetHandle = "left";
    } else {
      sourceHandle = "left";
      targetHandle = "right";
    }

    nodes.push({
      id: nodeId,
      type: "provider",
      position: { x: cx - nodeW / 2, y: cy - nodeH / 2 },
      data: { label: providerName(p), logo: PROVIDER_LOGO[p], textIcon: p.slice(0, 2).toUpperCase(), active },
      draggable: false,
    });
    edges.push({
      id: `e-${nodeId}`,
      type: "topology",
      source: "router",
      sourceHandle,
      target: nodeId,
      targetHandle,
      animated: false, // the particle beam replaces the CPU-heavy dash animation
      data: { active },
      style: edgeStyle(active, last, error),
    });
  });

  return { nodes, edges };
}

const fitOpts = { padding: 0.2, duration: 200 };

/**
 * Qanat doesn't track in-flight requests per provider (no hot-path writes,
 * PRD §9), so while anything streams the beam goes to the provider of the
 * most recent request.
 */
export function ProviderTopology({
  providers,
  activeCount,
  lastProvider,
  errorProvider,
}: {
  providers: string[];
  activeCount: number;
  lastProvider: string;
  errorProvider: string;
}) {
  const activeSet = useMemo(() => new Set(activeCount > 0 && lastProvider ? [lastProvider] : []), [activeCount, lastProvider]);
  const lastSet = useMemo(() => new Set(lastProvider ? [lastProvider] : []), [lastProvider]);
  const errorSet = useMemo(() => new Set(errorProvider ? [errorProvider] : []), [errorProvider]);

  const { nodes, edges } = useMemo(
    () => buildLayout(providers, activeSet, lastSet, errorSet, activeCount),
    [providers, activeSet, lastSet, errorSet, activeCount],
  );

  // Stable key — only remount when provider list changes
  const providersKey = useMemo(() => [...providers].sort().join(","), [providers]);

  const rfInstance = useRef<ReactFlowInstance | null>(null);
  const containerRef = useRef<HTMLDivElement>(null);
  const onInit = useCallback((instance: ReactFlowInstance) => {
    rfInstance.current = instance;
    setTimeout(() => instance.fitView(fitOpts), 50);
  }, []);

  // Re-fit on container resize
  useEffect(() => {
    const el = containerRef.current;
    if (!el) return;
    const ro = new ResizeObserver(() => rfInstance.current?.fitView(fitOpts));
    ro.observe(el);
    return () => ro.disconnect();
  }, []);

  // Re-fit when node count/layout changes
  useEffect(() => {
    if (!rfInstance.current) return;
    const id = setTimeout(() => rfInstance.current?.fitView(fitOpts), 50);
    return () => clearTimeout(id);
  }, [nodes.length]);

  return (
    <div ref={containerRef} className="h-[320px] w-full min-w-0 rounded-lg border bg-muted/30 sm:h-[480px]">
      {providers.length === 0 ? (
        <div className="flex h-full items-center justify-center text-sm text-muted-foreground">No providers connected</div>
      ) : (
        <ReactFlow
          key={providersKey}
          nodes={nodes}
          edges={edges}
          nodeTypes={nodeTypes}
          edgeTypes={edgeTypes}
          fitView
          fitViewOptions={fitOpts}
          minZoom={0.1}
          maxZoom={2}
          onInit={onInit}
          proOptions={{ hideAttribution: true }}
          panOnDrag
          zoomOnScroll={false}
          zoomOnPinch
          zoomOnDoubleClick
          preventScrolling={false}
          nodesDraggable={false}
          nodesConnectable={false}
          elementsSelectable={false}
          aria-label="Provider topology"
        >
          <Controls showInteractive={false} className="react-flow-controls-custom" />
        </ReactFlow>
      )}
    </div>
  );
}
