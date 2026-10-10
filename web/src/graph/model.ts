import type { ClusterGraph, GraphNode } from '../api/types'
export interface PlacedNode extends GraphNode {x: number; y: number}
export interface Region {name: string; x: number; y: number; width: number; height: number; count: number}
export interface Layout {nodes: PlacedNode[]; regions: Region[]; width: number; height: number}
export const NODE_W = 156
export const NODE_H = 48
export const routeEdge = (type: string) => type === 'routes-to' || type === 'selects'

/** Deterministic namespace islands and dependency columns; O(N log N + E).
 * No continuous force simulation: the camera never pays for layout. */
export function layoutGraph(graph: ClusterGraph): Layout {
 const groups = new Map<string, GraphNode[]>()
 const byID = new Map(graph.nodes.map(n => [n.id, n]))
 for (const node of graph.nodes) {
  const scope = node.ref.scope ?? ''
  const group = groups.get(scope) ?? []
  group.push(node); groups.set(scope, group)
 }
 const incoming = new Map<string, number>(), children = new Map<string, string[]>()
 for (const edge of graph.edges) {
  const a = byID.get(edge.source), b = byID.get(edge.target)
  if (!a || !b || a.ref.scope !== b.ref.scope || edge.source === edge.target) continue
  incoming.set(b.id, (incoming.get(b.id) ?? 0) + 1)
  const list = children.get(a.id) ?? []; list.push(b.id); children.set(a.id, list)
 }
 const ranks = new Map<string, number>()
 const queue = graph.nodes.filter(n => !incoming.has(n.id)).map(n => n.id)
 for (let i = 0; i < queue.length; i++) {
  const id = queue[i], rank = ranks.get(id) ?? 0
  for (const next of children.get(id) ?? []) {
   ranks.set(next, Math.max(ranks.get(next) ?? 0, Math.min(7, rank + 1)))
   const left = (incoming.get(next) ?? 1) - 1; incoming.set(next, left)
   if (!left) queue.push(next)
  }
 }
 const islands: {region: Region; nodes: PlacedNode[]}[] = []
 for (const [name, group] of [...groups].sort(([a], [b]) => a.localeCompare(b))) {
  const columns = new Map<string, GraphNode[]>()
  for (const node of group) {
   const key = String(ranks.get(node.id) ?? 0) + '/' + node.ref.kind
   const list = columns.get(key) ?? []; list.push(node); columns.set(key, list)
  }
  const cols = [...columns].sort(([a], [b]) => a.localeCompare(b))
  // Split tall columns to keep large namespaces navigable as an island.
  const rows = Math.max(4, Math.ceil(Math.sqrt(group.length * 2)))
  const nodes: PlacedNode[] = []; let col = 0, tallest = 0
  for (const [, list] of cols) {
   list.sort((a, b) => (a.ref.title ?? a.ref.name).localeCompare(b.ref.title ?? b.ref.name) || a.id.localeCompare(b.id))
   for (let i = 0; i < list.length; i++) {
    nodes.push({...list[i], x: 30 + (col + Math.floor(i / rows)) * 208, y: 64 + (i % rows) * 78})
   }
   tallest = Math.max(tallest, Math.min(rows, list.length))
   col += Math.ceil(list.length / rows)
  }
  islands.push({region: {name, x: 0, y: 0, width: Math.max(460, col * 208 + 8), height: Math.max(220, tallest * 78 + 86), count: group.length}, nodes})
 }
 const totalArea = islands.reduce((sum, i) => sum + i.region.width * i.region.height, 0)
 const rowWidth = Math.max(1000, Math.sqrt(totalArea * 1.7))
 const nodes: PlacedNode[] = [], regions: Region[] = []
 let x = 0, y = 0, rowHeight = 0, width = 0
 for (const island of islands) {
  if (x && x + island.region.width > rowWidth) {x = 0; y += rowHeight + 100; rowHeight = 0}
  regions.push({...island.region, x, y})
  for (const node of island.nodes) nodes.push({...node, x: node.x + x, y: node.y + y})
  x += island.region.width + 100; width = Math.max(width, x - 100); rowHeight = Math.max(rowHeight, island.region.height)
 }
 return {nodes, regions, width, height: y + rowHeight}
}
export interface Camera {x: number; y: number; zoom: number}
export function zoomAt(camera: Camera, factor: number, x: number, y: number): Camera {
 const zoom = Math.max(0.002, Math.min(4, camera.zoom * factor)), ratio = zoom / camera.zoom
 return {x: x - (x - camera.x) * ratio, y: y - (y - camera.y) * ratio, zoom}
}
export function fitCamera(layout: Layout, width: number, height: number): Camera {
 const zoom = Math.min(1.2, Math.max(0.002, Math.min((width - 64) / Math.max(1, layout.width), (height - 120) / Math.max(1, layout.height))))
 return {zoom, x: (width - layout.width * zoom) / 2, y: 64 + (height - 112 - layout.height * zoom) / 2}
}
