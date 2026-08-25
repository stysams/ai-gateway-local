import type { ClientID, Provider, Route } from "./types";

export interface CatalogModel {
  provider: string;
  key_id: string;
  model: string;
  id: string;
}

export const CLIENT_ROUTE_IDS: ClientID[] = ["codex", "claude", "claude-desktop", "grok", "generic"];
export const EMPTY_ROUTE: Route = { provider: "", key_id: "", model: "" };

// enabledCatalog is the flat list a client picker must show: every enabled
// model of every enabled provider, labeled as <provider-id>/<model-id>.
export function enabledCatalog(providers: Array<Pick<Provider, "id" | "enabled" | "key_groups">>): CatalogModel[] {
  const out: CatalogModel[] = [];
  const seen = new Set<string>();
  for (const provider of providers) {
    if (provider.enabled === false) continue;
    for (const [key_id, group] of Object.entries(provider.key_groups || {})) {
      if (group.enabled === false) continue;
      for (const model of group.models || []) {
        if (model.enabled === false || !model.id) continue;
        const id = catalogId({ provider: provider.id, key_id, model: model.id });
        if (seen.has(id)) continue;
        seen.add(id);
        out.push({ provider: provider.id, key_id, model: model.id, id });
      }
    }
  }
  return out;
}

export function catalogId(route: Route | undefined): string {
  if (!route?.provider || !route.model) return "";
  return `${route.provider}/${route.key_id}/${route.model}`;
}

export function isCatalogRoute(route: Route | undefined, catalog: CatalogModel[]): boolean {
  const id = catalogId(route);
  return Boolean(id) && catalog.some((item) => item.id === id);
}

export function visibleRoute(route: Route | undefined, catalog: CatalogModel[]): Route {
  return isCatalogRoute(route, catalog) && route ? { provider: route.provider, key_id: route.key_id, model: route.model } : EMPTY_ROUTE;
}

// Keep an unsaved picker value only while it still exists in the enabled
// catalog. A disabled provider or model drops the selected default route.
export function reconcileClientRoutes(
  draft: Record<string, Route>,
  saved: Record<string, Route>,
  catalog: CatalogModel[],
  clients: readonly ClientID[] = CLIENT_ROUTE_IDS,
): Record<ClientID, Route> {
  const next = {} as Record<ClientID, Route>;
  for (const client of clients) {
    next[client] = isCatalogRoute(draft[client], catalog) ? { provider: draft[client].provider, key_id: draft[client].key_id, model: draft[client].model } : visibleRoute(saved[client], catalog);
  }
  return next;
}
