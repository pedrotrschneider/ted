import {useEffect, useRef, useState} from 'react';
import {api, errorMessage} from './api';
import type {User, ProfileChange} from './api';

export function AccountAvatar({user, updated}: {user: User; updated: (change: ProfileChange) => void}) {
  const input = useRef<HTMLInputElement>(null);
  const alive = useRef(false), uploading = useRef(false);
  const [pending, setPending] = useState(false), [error, setError] = useState('');
  const [failedVersion, setFailedVersion] = useState<string>();
  useEffect(() => {alive.current = true; return () => {alive.current = false;};}, []);
  async function upload(file?: File) {
    if (!file || uploading.current) return;
    if (file.size > 20 * 1024 * 1024) {setError('Profile pictures must be at most 20 MiB.'); return;}
    uploading.current = true; setPending(true); setError('');
    try {
      const changed = await api.uploadAvatar(file);
      if (alive.current) {setFailedVersion(undefined); updated({id: changed.id, avatar_version: changed.avatar_version});}
    } catch (e) {if (alive.current) setError(errorMessage(e));}
    finally {uploading.current = false; if (alive.current) setPending(false);}
  }
  return <div className="profile-picture">
    <div className="avatar-editor">
      <div className="avatar" aria-hidden="true">
        {user.avatar_version && failedVersion !== user.avatar_version
          ? <img src={`/api/account/avatar?v=${user.avatar_version}`} alt="" onError={() => setFailedVersion(user.avatar_version)}/>
          : user.display_name.slice(0, 1).toUpperCase()}
      </div>
      <button type="button" className="avatar-edit" aria-label="Change profile picture" title="Change profile picture" disabled={pending} onClick={() => input.current?.click()}>
        <svg viewBox="0 0 24 24" aria-hidden="true" fill="none" stroke="currentColor" strokeWidth="2"><path d="m15 5 4 4M4 20l4-1L20 7a2.8 2.8 0 0 0-4-4L4 15z"/></svg>
      </button>
      <input ref={input} type="file" accept="image/jpeg,image/png,image/webp,image/gif" aria-label="Upload profile picture" hidden disabled={pending} onChange={e => {const file = e.currentTarget.files?.[0]; e.currentTarget.value = ''; void upload(file);}}/>
    </div>
    {pending && <p className="hint" role="status">Saving picture…</p>}
    {error && <p className="notice error" role="alert">{error}</p>}
  </div>;
}
