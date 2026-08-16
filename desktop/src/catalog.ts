import type { Provider, Route } from "./types";

export interface CatalogModel {
  provider: string;
  model: string;
  id: string;
}

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

export function catalogId(route: Route): string {
  return `${route.provider}/${route.model}`;
}
