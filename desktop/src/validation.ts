import type { ProviderModel } from "./types";

export interface RequestHeader { name: string; value: string }
export type DisguiseClient = "" | "claude" | "codex";
export interface ProviderFormValue { id: string; name: string; adapter: string; base_url: string; models_url?: string; extra_headers: RequestHeader[]; disguise_client: DisguiseClient; default_model: string; models: ProviderModel[]; api_key: string }

const headerNamePattern = /^[!#$%&'*+\-.^_`|~0-9A-Za-z]+$/;
const forbiddenHeaders = new Set(["api-key", "authorization", "connection", "content-length", "cookie", "host", "proxy-authorization", "proxy-connection", "set-cookie", "te", "trailer", "transfer-encoding", "upgrade", "x-api-key"]);

export function validateProvider(value: ProviderFormValue, editing = false): Record<string, string> {
  const errors: Record<string, string> = {};
  if (!editing && !/^[a-z][a-z0-9_-]{0,31}$/.test(value.id)) errors.id = "invalid_id";
  if (!value.name.trim()) errors.name = "required";
  if (!value.default_model.trim()) errors.default_model = "required";
  if (!["openai-chat", "openai-responses", "anthropic"].includes(value.adapter)) errors.adapter = "invalid_adapter";
  if (value.disguise_client && !["claude", "codex"].includes(value.disguise_client)) errors.disguise_client = "invalid_disguise_client";
  const headerNames = new Set<string>();
  value.extra_headers.forEach((header, index) => {
    const name = header.name.trim();
    const normalized = name.toLowerCase();
    if (!name || !headerNamePattern.test(name)) errors[`extra_headers.${index}.name`] = "invalid_header_name";
    else if (forbiddenHeaders.has(normalized)) errors[`extra_headers.${index}.name`] = "managed_header";
    else if (headerNames.has(normalized)) errors[`extra_headers.${index}.name`] = "duplicate_header";
    else headerNames.add(normalized);
    if (header.value.length > 8192 || /[\r\n\0]/.test(header.value)) errors[`extra_headers.${index}.value`] = "invalid_header_value";
  });
  const modelIDs = new Set<string>();
  value.models.forEach((model, index) => {
    const id = model.id.trim();
    if (!id) errors[`models.${index}.id`] = "required";
    else if (modelIDs.has(id)) errors[`models.${index}.id`] = "duplicate_model";
    else modelIDs.add(id);
    if (model.adapter && !["openai-chat", "openai-responses", "anthropic"].includes(model.adapter)) errors[`models.${index}.adapter`] = "invalid_adapter";
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
