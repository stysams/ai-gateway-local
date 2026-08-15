import type { ProviderModel } from "./types";

export interface ProviderFormValue { id: string; name: string; adapter: string; base_url: string; models_url?: string; default_model: string; models: ProviderModel[]; api_key: string }

export function validateProvider(value: ProviderFormValue, editing = false): Record<string, string> {
  const errors: Record<string, string> = {};
  if (!editing && !/^[a-z][a-z0-9_-]{0,31}$/.test(value.id)) errors.id = "invalid_id";
  if (!value.name.trim()) errors.name = "required";
  if (!value.default_model.trim()) errors.default_model = "required";
  if (!["openai-chat", "openai-responses", "anthropic"].includes(value.adapter)) errors.adapter = "invalid_adapter";
  const modelIDs = new Set<string>();
  value.models.forEach((model, index) => {
    const id = model.id.trim();
    if (!id) errors[`models.${index}.id`] = "required";
    else if (modelIDs.has(id)) errors[`models.${index}.id`] = "duplicate_model";
    else modelIDs.add(id);
    if (model.context_window < 0) errors[`models.${index}.context_window`] = "invalid_token_limit";
    if (model.max_output_tokens < 0) errors[`models.${index}.max_output_tokens`] = "invalid_token_limit";
  });
  if (value.models.length > 0 && !modelIDs.has(value.default_model.trim())) errors.default_model = "default_model_missing";
  try {
    const url = new URL(value.base_url);
    if (!['http:', 'https:'].includes(url.protocol)) errors.base_url = "invalid_url";
  } catch { errors.base_url = "invalid_url"; }
  const modelsURL = value.models_url?.trim() || "";
  if (modelsURL) {
    try {
      const url = new URL(modelsURL);
      if (!['http:', 'https:'].includes(url.protocol)) errors.models_url = "invalid_url";
    } catch { errors.models_url = "invalid_url"; }
  }
  return errors;
}
