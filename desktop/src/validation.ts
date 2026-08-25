import { isModelAdapter, validateCustomEndpoint } from "./endpoint";
import type { KeyGroup, ProviderModel } from "./types";

export interface RequestHeader { name: string; value: string }
export type DisguiseClient = "" | "claude" | "codex" | "pi";
export interface ProviderFormValue { id: string; name: string; base_url: string; models_url?: string; extra_headers: RequestHeader[]; disguise_client: DisguiseClient; key_groups: Record<string, KeyGroup> }

const headerNamePattern = /^[!#$%&'*+\-.^_`|~0-9A-Za-z]+$/;
const forbiddenHeaders = new Set(["api-key", "authorization", "connection", "content-length", "cookie", "host", "proxy-authorization", "proxy-connection", "set-cookie", "te", "trailer", "transfer-encoding", "upgrade", "x-api-key"]);

export function validateProvider(value: ProviderFormValue, editing = false): Record<string, string> {
  const errors: Record<string, string> = {};
  if (!editing && !/^[a-z][a-z0-9_-]{0,31}$/.test(value.id)) errors.id = "invalid_id";
  if (!value.name.trim()) errors.name = "required";
  if (value.disguise_client && !["claude", "codex", "pi"].includes(value.disguise_client)) errors.disguise_client = "invalid_disguise_client";
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
  const groupIDs = new Set<string>();
  if (Object.keys(value.key_groups).length === 0) errors.key_groups = "required";
  Object.entries(value.key_groups).forEach(([keyID, group]) => {
    if (!/^[a-z][a-z0-9_-]{0,31}$/.test(keyID)) errors[`key_groups.${keyID}`] = "invalid_id";
    if (groupIDs.has(keyID)) errors[`key_groups.${keyID}`] = "duplicate_key_group";
    groupIDs.add(keyID);
    if (!group.name.trim()) errors[`key_groups.${keyID}.name`] = "required";
    if (!group.default_model.trim()) errors[`key_groups.${keyID}.default_model`] = "required";
    if (group.endpoint?.trim()) {
      const endpointError = validateCustomEndpoint(group.endpoint);
      if (endpointError) errors[`key_groups.${keyID}.endpoint`] = endpointError;
    }
    const modelIDs = new Set<string>();
    group.models.forEach((model, index) => {
      const id = model.id.trim();
      if (!id) errors[`key_groups.${keyID}.models.${index}.id`] = "required";
      else if (modelIDs.has(id)) errors[`key_groups.${keyID}.models.${index}.id`] = "duplicate_model";
      else modelIDs.add(id);
      if (model.adapter && !isModelAdapter(model.adapter)) errors[`key_groups.${keyID}.models.${index}.adapter`] = "invalid_adapter";
      if (model.adapter === "custom") {
        const endpointError = validateCustomEndpoint(model.endpoint);
        if (endpointError) errors[`key_groups.${keyID}.models.${index}.endpoint`] = endpointError;
      } else if (model.endpoint?.trim() && model.adapter) {
        errors[`key_groups.${keyID}.models.${index}.endpoint`] = "preset_endpoint_locked";
      }
    });
    if (!modelIDs.has(group.default_model.trim())) errors[`key_groups.${keyID}.default_model`] = "default_model_missing";
  });
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
