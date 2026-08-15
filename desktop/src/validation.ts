export interface ProviderFormValue { id: string; name: string; adapter: string; base_url: string; default_model: string; api_key: string }

export function validateProvider(value: ProviderFormValue, editing = false): Record<string, string> {
  const errors: Record<string, string> = {};
  if (!editing && !/^[a-z0-9][a-z0-9-]{0,62}$/.test(value.id)) errors.id = "invalid_id";
  if (!value.name.trim()) errors.name = "required";
  if (!value.default_model.trim()) errors.default_model = "required";
  if (!["openai-chat", "openai-responses", "anthropic"].includes(value.adapter)) errors.adapter = "invalid_adapter";
  try {
    const url = new URL(value.base_url);
    if (!['http:', 'https:'].includes(url.protocol)) errors.base_url = "invalid_url";
  } catch { errors.base_url = "invalid_url"; }
  return errors;
}
