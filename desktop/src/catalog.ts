import type { ClientID, Provider, Route } from "./types";

export interface CatalogModel {
  provider: string;
  model: string;
  id: string;
}

export const CLIENT_ROUTE_IDS: ClientID[] = ["codex", "claude", "claude-desktop", "grok", "generic"];
export const EMPTY_ROUTE: Route = { provider: "", model: "" };

// enabledCatalog is the flat list a client picker must show: every enabled
// model of every enabled provider, labeled as <provider-id>/<model-id>.
export function enabledCatalog(providers: Array<Pick<Provider, "id" | "enabled" | "default_model" | "models">>): CatalogModel[] {
  const out: CatalogModel[] = [];
  const seen = new Set<string>();
  for (const provider of providers) {
    if (provider.enabled === false) continue;
    const raw = provider.models?.length
      ? provider.models.filter((model) => model.enabled !== false).map((model) => model.id)
      : provider.default_model ? [provider.default_model] : [];
    for (const model of raw) {
      if (!model) continue;
      const id = catalogId({ provider: provider.id, model });
      if (seen.has(id)) continue;
      seen.add(id);
      out.push({ provider: provider.id, model, id });
    }
  }
  return out;
}

export function catalogId(route: Route | undefined): string {
  if (!route?.provider || !route.model) return "";
  return `${route.provider}/${route.model}`;
}

export function isCatalogRoute(route: Route | undefined, catalog: CatalogModel[]): boolean {
  const id = catalogId(route);
  return Boolean(id) && catalog.some((item) => item.id === id);
}

export function visibleRoute(route: Route | undefined, catalog: CatalogModel[]): Route {
  return isCatalogRoute(route, catalog) && route ? { provider: route.provider, model: route.model } : EMPTY_ROUTE;
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
    next[client] = isCatalogRoute(draft[client], catalog) ? { provider: draft[client].provider, model: draft[client].model } : visibleRoute(saved[client], catalog);
  }
  return next;
}
