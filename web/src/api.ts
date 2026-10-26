export interface User { id: number; username: string; display_name: string; role: 'admin' | 'user'; disabled: boolean; avatar_version?: string }
export type ProfileChange = Pick<User, 'id'> & Partial<Pick<User, 'display_name' | 'avatar_version'>>;
export interface UserPage { users: User[]; next_after_id?: number }
export class ApiError extends Error {
  status: number;
  constructor(message: string, status = 0) { super(message); this.name = 'ApiError'; this.status = status; }
}
const record = (v: unknown): v is Record<string, unknown> => typeof v === 'object' && v !== null;
function user(v: unknown): User {
  if (record(v) && v.avatar_version !== undefined && (typeof v.avatar_version !== 'string' || !/^[a-f0-9]{64}$/.test(v.avatar_version))) throw new ApiError('TED returned an invalid profile picture. Reload to try again.');
  if (!record(v) || !Number.isSafeInteger(v.id) || Number(v.id) <= 0 || typeof v.username !== 'string' || typeof v.display_name !== 'string' || !v.display_name.trim() || !['admin', 'user'].includes(String(v.role)) || typeof v.disabled !== 'boolean') throw new ApiError('TED returned an invalid account. Reload to try again.');
  return v as unknown as User;
}
export async function request(path: string, method = 'GET', body?: unknown, signal?: AbortSignal): Promise<unknown> {
  const controller = new AbortController();
  const cancel = () => controller.abort();
  if (signal?.aborted) cancel();
  signal?.addEventListener('abort', cancel, { once: true });
  const timer = setTimeout(cancel, 15000);
  try {
    const response = await fetch(path, { method, credentials: 'same-origin', cache: 'no-store', signal: controller.signal,
      headers: body === undefined ? { Accept: 'application/json' } : { Accept: 'application/json', 'Content-Type': body instanceof Blob ? body.type || 'application/octet-stream' : 'application/json' },
      body: body === undefined ? undefined : body instanceof Blob ? body : JSON.stringify(body) });
    if (!response.ok) {
      const rdMessages: Record<string,string> = {
        avatar_invalid:'Choose a valid JPEG, PNG, WebP or GIF up to 20 MiB and 40 megapixels.',
        avatar_too_large:'Profile pictures must be at most 20 MiB.',
        artwork_changed:'Artwork changed while the picker was open. Refresh the artwork and choose again.',
        artwork_invalid:'Choose a valid JPEG, PNG or GIF up to 20 MiB and 40 megapixels.',
        artwork_too_large:'Artwork must be at most 20 MiB.',
        metadata_not_configured:'Configure the required metadata provider and artwork storage in server settings.',
		metadata_overrides_review_required:'Manual overrides changed. Reopen the provider confirmation and choose whether to keep or remove them.',
        metadata_provider_changed:'The metadata provider changed. Reload the series before trying again.',
		metadata_identity_ambiguous:'The provider’s episode identities could not be verified. Existing history and mappings are preserved.',
		metadata_not_found:'The selected metadata provider has no verified match for this item.',
        stremio_invalid:'Check the Stremio connection fields.',
        stremio_copy_same_account:'Source and destination must be different Stremio accounts.',
        stremio_copy_missing:'This account-copy run no longer exists. Refresh its status.',
        stremio_copy_active:'Finish or stop the existing copy first. An account cannot be a source while another active copy writes to it.',
        stremio_copy_finished:'This copy has already finished or stopped. Refresh its status; enter both accounts to start a new copy.',
        stremio_credentials:'Stremio rejected the email or password.',
        stremio_changed:'The Stremio connection, preview or selection changed. Refresh and review before trying again. Disconnect first if linking a different account.',
        stremio_unavailable:'Stremio is unavailable or returned incomplete data. Try again later.',
        stremio_worker_unavailable:'Configure the TED background worker before reading a Stremio preview.',
        watch_invalid: 'Choose a valid watched-state action.',
        watch_conflict: 'The saved state or profile link changed, or a change is still pending. Refresh and review before trying again.',
        watch_unavailable: 'Viewing state is unavailable. Refresh the title and try again.',
        lifecycle_changed:'The library mode changed. Restart TED with consistent configuration; finish pending work in its original mode before switching.',
        stream_unconfigured: 'TED media workers are unavailable. Check server configuration and diagnostics.',
        catalog_unavailable:'Cinemeta is unavailable. Try again later.',
        catalog_incomplete:'Cinemeta returned incomplete data. Previous results are retained; try again later.',
        catalog_invalid:'Invalid movie or file selection.',
        catalog_not_found:'This movie is not available from Cinemeta.',
        mapping_changed:'The selected source changed. Refresh its files and select again.',
        mapping_conflict:'Another mapping operation is pending, or the saved library conflicts with this selection. Refresh the mapping status and review your selection.',
        rd_not_configured:'An administrator can configure Real-Debrid in Your account → Server providers.',
        settings_changed:'Settings changed. Reload their status before saving again.',
        settings_invalid:'Check the provider credential fields. Values must not contain whitespace.',
        rd_credentials:'Real-Debrid rejected the configured key. Check the key and RD account status.',
        rd_limited:'Real-Debrid is rate limiting requests. Wait before refreshing.',
        rd_incomplete:'Real-Debrid returned incomplete data. Your previous results are retained; refresh to try again.',
        rd_not_found:'This torrent is no longer available in the connected RD account.',
        rd_unavailable:'Real-Debrid is unavailable. Try again later.',
        rd_cache_pending:'Real-Debrid inventory is being built. Previous results are preserved; try again shortly.',
        rd_invalid:'Invalid torrent or page.'
      };
      let details: unknown;
      try {details=await response.json();} catch { /* Use the status fallback below. */ }
      if (record(details) && typeof details.code==='string' && rdMessages[details.code]) throw new ApiError(rdMessages[details.code],response.status);
      const messages: Record<number, string> = { 400: 'Check the fields and try again.', 401: 'Your credentials or session were not accepted.', 403: 'This action is not allowed for your account.', 404: 'This account no longer exists.', 409: 'The username is already taken, or this change would leave TED without an active administrator.', 429: 'Too many sign-in attempts. Wait ten minutes before trying again.' };
      const unavailable = method === 'GET'
        ? 'TED is unavailable. Try again when the service is ready.'
        : 'TED could not confirm the change. It may have completed. Check the current state before trying again.';
      throw new ApiError(messages[response.status] ?? unavailable, response.status);
    }
    try { return await response.json(); } catch { throw new ApiError('TED returned an unreadable response. Refresh to check the current state.'); }
  } catch (error) {
    if (error instanceof ApiError) throw error;
    if (signal?.aborted) throw new DOMException('Request cancelled', 'AbortError');
    throw new ApiError(method === 'GET' ? 'Cannot reach TED. Check your connection and try again.' : 'No confirmation received. The change may have completed. Check the current state before trying again.');
  } finally { clearTimeout(timer); signal?.removeEventListener('abort', cancel); }
}
async function acknowledge(path: string, method: string, body?: unknown): Promise<void> {
  const result = await request(path, method, body);
  if (!record(result) || typeof result.status !== 'string' || !result.status) {
    throw new ApiError('TED returned an invalid confirmation. Check the current state before trying again.');
  }
}
export function validatePassword(password: string, minimum: number): void {
  if (Array.from(password).length < minimum || new TextEncoder().encode(password).length > 1024) throw new ApiError(`Password must have at least ${minimum} characters and at most 1,024 UTF-8 bytes.`);
}
export const api = {
  setupRequired: async (signal?: AbortSignal): Promise<boolean> => {
    const v = await request('/api/auth/setup', 'GET', undefined, signal);
    if (!record(v) || typeof v.setup_required !== 'boolean') throw new ApiError('TED returned invalid setup information.');
    return v.setup_required;
  },
  setup: async (username: string, password: string) => {
    const v = await request('/api/auth/setup', 'POST', {username, password});
    if (!record(v) || v.status !== 'administrator created') throw new ApiError('TED returned an invalid setup confirmation. Refresh to check whether setup completed.');
  },
  policy: async (signal?: AbortSignal): Promise<number> => {
    const v = await request('/api/auth/policy', 'GET', undefined, signal);
    if (!record(v) || !Number.isInteger(v.min_password_length) || Number(v.min_password_length)<0 || Number(v.min_password_length)>1024) throw new ApiError('TED returned an invalid password policy. Reload to try again.');
    return Number(v.min_password_length);
  },
  profile: async (display_name: string) => user(await request('/api/auth/profile', 'PUT', { display_name })),
  uploadAvatar: async (image: Blob) => {
    const updated = user(await request('/api/account/avatar', 'PUT', image));
    if (!updated.avatar_version) throw new ApiError('TED did not confirm the profile picture. Reload to check the current picture.');
    return updated;
  },
  me: async (signal?: AbortSignal) => user(await request('/api/auth/me', 'GET', undefined, signal)),
  login: async (username: string, password: string) => {
    user(await request('/api/auth/login', 'POST', { username, password }));
    try { return await api.me(); } catch (error) {
      if (error instanceof ApiError && error.status === 401) throw new ApiError('Sign-in could not retain its session. Use HTTPS, or ask the administrator to check TED_INSECURE_HTTP for local testing.');
      throw error;
    }
  },
  logout: () => acknowledge('/api/auth/logout-browser', 'POST'),
  logoutAll: () => acknowledge('/api/auth/logout', 'POST'),
  password: (current_password: string, new_password: string) => acknowledge('/api/auth/password', 'POST', { current_password, new_password }),
  createUser: async (username: string, display_name: string, password: string, role: User['role']) => user(await request('/api/admin/users', 'POST', { username, display_name, password, role })),
  setDisabled: (id: number, disabled: boolean) => acknowledge(`/api/admin/users/${id}/disabled`, 'PUT', { disabled }),
  users: async (after?: number, signal?: AbortSignal): Promise<UserPage> => {
    const v = await request('/api/admin/users' + (after === undefined ? '' : `?after=${after}`), 'GET', undefined, signal);
    if (!record(v) || !Array.isArray(v.users)) throw new ApiError('TED returned an incomplete account list. Reload to try again.');
    const users = v.users.map(user);
    if (users.some((u, i) => u.id <= (i ? users[i - 1].id : after ?? 0)) ||
      (v.next_after_id !== undefined && (!Number.isSafeInteger(v.next_after_id) || users.at(-1)?.id !== v.next_after_id))) throw new ApiError('TED returned an invalid account page. Reload to try again.');
    return { users, next_after_id: v.next_after_id as number | undefined };
  },
};
export const errorMessage = (error: unknown) => error instanceof ApiError ? error.message : 'Something went wrong. Reload and try again.';
