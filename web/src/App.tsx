import {UpdateNotice} from './UpdateNotice';
import {JointAccounts} from './JointAccounts';
import {StremioConnection} from './StremioConnection';
import {AddonSettings} from './AddonSettings';
import {ProviderSettings} from './ProviderSettings';
import {StremioCopy} from './StremioCopy';
import { useEffect, useRef, useState } from 'react';
import type { FormEvent, ReactNode } from 'react';
import { api, ApiError, errorMessage, validatePassword } from './api';
import type { User } from './api';
import { Sources } from './Sources';
import { MediaBrowser } from './MediaBrowser';
import { WatchHistory } from './WatchHistory';
import {useRouteScroll} from './useRouteScroll';
import { pageFromHash } from './navigation.ts';

function Brand() { return <a className="brand" href="/" aria-label="The Entertainment District home"><span className="mark">TED<span>●</span></span><span>The Entertainment<br/>District</span></a>; }
function Notice({ children, error = false }: { children: ReactNode; error?: boolean }) { return children ? <p className={error ? 'notice error' : 'notice'} role={error ? 'alert' : 'status'}>{children}</p> : null; }
function Password({ name, label, fresh = false }: { name: string; label: string; fresh?: boolean }) { return <label>{label}<input name={name} type="password" autoComplete={fresh ? 'new-password' : 'current-password'}  maxLength={1024}/></label>; }
const field = (form: HTMLFormElement, name: string) => String(new FormData(form).get(name) ?? '');
function useAction() {
  const alive = useRef(false), busy = useRef(false);
  const [pending, setPending] = useState(false), [error, setError] = useState('');
  useEffect(() => { alive.current = true; return () => { alive.current = false; }; }, []);
  async function run(work: () => Promise<unknown>, done: () => void) {
    if (busy.current) return;
    busy.current = true; setPending(true); setError('');
    try { await work(); if (alive.current) done(); }
    catch (e) { if (alive.current) setError(errorMessage(e)); }
    finally { busy.current = false; if (alive.current) setPending(false); }
  }
  return { pending, error, run, setError };
}
export function App() {
  const [minimum, setMinimum] = useState(0);
  const [setupRequired, setSetupRequired] = useState(false);
  const [user, setUser] = useState<User | null>(null), [checking, setChecking] = useState(true), [failure, setFailure] = useState(''), [attempt, setAttempt] = useState(0), [message, setMessage] = useState('');
  useEffect(() => {
    const controller = new AbortController(); setChecking(true); setFailure('');
    Promise.all([api.setupRequired(controller.signal), api.policy(controller.signal), api.me(controller.signal).catch(e => {
      if (e instanceof ApiError && e.status === 401) return null; throw e;
    })]).then(([setup, policy, u]) => { if (!controller.signal.aborted) {setSetupRequired(setup); setMinimum(policy); setUser(u);} }).catch(e => {
      if (controller.signal.aborted) return;
      if (e instanceof ApiError && e.status === 401) setUser(null); else setFailure(errorMessage(e));
    }).finally(() => { if (!controller.signal.aborted) setChecking(false); });
    return () => controller.abort();
  }, [attempt]);
  function signedOut(message: string) { setUser(null); setMessage(message); }
  return <><a className="skip" href="#main" onClick={e => { e.preventDefault(); document.getElementById('main')?.focus(); }}>Skip to content</a>{checking || failure ? <main tabIndex={-1} id="main" className="connection"><Brand/><h1>{checking ? 'Opening your district…' : 'Unable to connect'}</h1><Notice error>{failure}</Notice>{failure && <button onClick={() => setAttempt(attempt + 1)}>Try again</button>}</main> : setupRequired ? <Setup minimum={minimum} complete={message => {setSetupRequired(false); setMessage(message);}}/> : user ? <AccountShell key={user.id} user={user} minimum={minimum} updated={setUser} signedOut={signedOut}/> : <Login message={message} onLogin={u => { setUser(u); setMessage(''); }}/>}<footer>THE ENTERTAINMENT DISTRICT <span>Personal by design.</span><a href="/third-party-notices.txt">Open-source notices</a></footer></>;
}
function Setup({minimum, complete}: {minimum: number; complete: (message: string) => void}) {
  const action = useAction();
  function submit(e: FormEvent<HTMLFormElement>) {
    e.preventDefault(); const form = e.currentTarget;
    const password = field(form, 'password');
    if (password !== field(form, 'confirm')) { action.setError('Passwords do not match.'); return; }
    void action.run(async () => {
      validatePassword(password, minimum);
      try { await api.setup(field(form, 'username'), password); }
      catch (error) {
        if (error instanceof ApiError && error.status === 409) {
          complete('Setup has already been completed. Sign in with an existing account.'); return;
        }
        throw error;
      }
      complete('Administrator created. Sign in with your new account.');
    }, () => {});
  }
  return <div className="welcome"><section className="welcome-copy"><Brand/><div><p className="eyebrow">WELCOME TO TED</p><h1>Your district<br/>starts here.</h1><p className="intro">Create the first administrator to set up your server.</p></div></section><main tabIndex={-1} id="main" className="login-panel"><div className="form-heading"><h2>Create administrator</h2><p>This setup is available only while the server has no accounts.</p></div><form onSubmit={submit}><fieldset disabled={action.pending}><label>Username<input name="username" autoComplete="username" autoCapitalize="none" spellCheck={false} required minLength={3} maxLength={64} pattern="[A-Za-z0-9][A-Za-z0-9_.\-]{2,63}"/></label><Password name="password" label="Password" fresh/><Password name="confirm" label="Confirm password" fresh/><p className="hint">{minimum > 0 ? `Use at least ${minimum} characters.` : 'A password is optional under this server’s password policy.'}</p><Notice error>{action.error}</Notice><button type="submit" className="primary">{action.pending ? 'Creating administrator…' : 'Create administrator'}</button></fieldset></form><p className="form-note">Already completed setup in another window? <button onClick={() => complete('Sign in with your administrator account.')}>Go to sign in</button></p></main></div>;
}
function Login({ message, onLogin }: { message: string; onLogin: (u: User) => void }) {
  const action = useAction();
  function submit(e: FormEvent<HTMLFormElement>) {
    e.preventDefault(); const form = e.currentTarget; let user: User;
    void action.run(async () => { user = await api.login(field(form, 'username'), field(form, 'password')); }, () => { form.reset(); onLogin(user); });
  }
  return <div className="welcome"><section className="welcome-copy"><Brand/><div><p className="eyebrow">YOUR PERSONAL DISTRICT</p><h1>A place for <br/>your next <br/><em>great watch.</em></h1><p className="intro">Welcome to The Entertainment District.<br/>Your space. Your account.</p></div><p className="quiet">Discover, watch and keep track of your movies and shows.</p></section><main tabIndex={-1} id="main" className="login-panel"><div className="form-heading"><span className="eyebrow">WELCOME BACK</span><h2>Sign in to TED</h2><p>Use your local TED account to continue.</p></div><Notice>{message}</Notice><form onSubmit={submit}><fieldset disabled={action.pending}><label>Username<input name="username" autoComplete="username" required maxLength={64} autoCapitalize="none" spellCheck={false}/></label><Password name="password" label="Password"/><Notice error>{action.error}</Notice><button className="primary" type="submit">{action.pending ? 'Signing in…' : 'Sign in'}<span aria-hidden="true">↗</span></button></fieldset></form><p className="form-note">Need an account? Ask your TED administrator.</p></main></div>;
}
function AccountShell({ user, signedOut, minimum, updated }: { user: User; minimum: number; updated: (user: User) => void; signedOut: (message: string) => void }) {
  const [hash, setHash] = useState(() => window.location.hash);
  useRouteScroll(hash);
  useEffect(() => { const changed = () => setHash(window.location.hash); window.addEventListener('hashchange', changed); return () => window.removeEventListener('hashchange', changed); }, []);
  const page = pageFromHash(hash, user.role === 'admin'); const logout = useAction();
  return <div className="shell">{user.role === 'admin' && <UpdateNotice/>}<header><Brand/><div className="header-account"><span>{user.display_name}</span><button className="subtle" disabled={logout.pending} onClick={() => void logout.run(api.logout, () => signedOut('You have been signed out of this browser.'))}>{logout.pending ? 'Signing out…' : 'Sign out'}</button></div></header><Notice error>{logout.error}</Notice><nav aria-label="Main navigation"><a href="#movies" aria-current={page === 'movies' ? 'page' : undefined}>Movies</a><a href="#series" aria-current={page === 'series' ? 'page' : undefined}>Series</a><a href="#history" aria-current={page === 'history' ? 'page' : undefined}>Watch history</a><a href="#account" aria-current={page === 'account' ? 'page' : undefined}>Your account</a>{user.role === 'admin' && <><a href="#people" aria-current={page === 'people' ? 'page' : undefined}>People</a><a href="#sources" aria-current={page === 'sources' ? 'page' : undefined}>Sources</a><a href="#stremio-sync" aria-current={page === 'stremio-sync' ? 'page' : undefined}>Stremio account sync</a></>}</nav><main tabIndex={-1} id="main">{page === 'movies' || page === 'series' ? <MediaBrowser key={page} hash={hash} kind={page === 'movies' ? 'movie' : 'series'} admin={user.role === 'admin'} expired={() => signedOut('Your session has expired. Please sign in again.')}/> : page === 'history' ? <WatchHistory expired={() => signedOut('Your session has expired. Please sign in again.')}/> : page === 'account' ? <Profile user={user} minimum={minimum} updated={updated} signedOut={signedOut}/> : page === 'stremio-sync' ? <StremioCopy expired={() => signedOut('Your session has expired. Please sign in again.')}/> : page === 'people' ? <People user={user} minimum={minimum} signedOut={signedOut}/> : <Sources expired={() => signedOut('Your session has expired. Please sign in again.')}/>}</main></div>;
}
function Profile({ user, signedOut, minimum, updated }: { user: User; minimum: number; updated: (user: User) => void; signedOut: (message: string) => void }) {
  const action = useAction();
  function submit(e: FormEvent<HTMLFormElement>) {
    e.preventDefault(); const form = e.currentTarget;
    if (field(form, 'new') !== field(form, 'confirm')) { action.setError('The new passwords do not match.'); return; }
    void action.run(() => { validatePassword(field(form, 'new'), minimum); return api.password(field(form, 'current'), field(form, 'new')); }, () => signedOut('Password changed. Sign in again with your new password. All other sessions were signed out.'));
  }
  return <><div className="page-heading"><p className="eyebrow">MAKE YOURSELF AT HOME</p><h1>Your account</h1><p>Manage your access to The Entertainment District.</p></div><div className="panel-stack account-settings"><DisplayName user={user} updated={updated}/><div className="columns account-columns"><section className="card identity"><div className="avatar" aria-hidden="true">{user.display_name.slice(0, 1).toUpperCase()}</div><h2>{user.display_name}</h2><p>@{user.username}</p><span className="badge">{user.role === 'admin' ? 'Administrator' : 'Member'}</span><dl><dt>Account</dt><dd>Local to TED</dd><dt>Status</dt><dd>Active</dd></dl><p className="quiet">Your TED login is independent of connected services.</p><button className="subtle" disabled={action.pending} onClick={() => void action.run(api.logoutAll, () => signedOut('You have been signed out on all devices.'))}>Sign out all devices</button></section><section className="card"><h2>Change password</h2><p>Changing your password signs you out on every device.</p><form onSubmit={submit}><fieldset disabled={action.pending}><Password name="current" label="Current password"/><Password name="new" label="New password" fresh/><Password name="confirm" label="Confirm new password" fresh/><p className="hint">{minimum === 0 ? "A password is optional." : `Minimum ${minimum} characters.`} Maximum 1,024 UTF-8 bytes.</p><Notice error>{action.error}</Notice><button className="primary" type="submit">{action.pending ? 'Saving…' : 'Update password'}</button></fieldset></form></section></div>{user.role==='admin'&&<ProviderSettings expired={()=>signedOut('Your session has expired. Please sign in again.')}/>}<JointAccounts expired={()=>signedOut('Your session has expired. Please sign in again.')}/><StremioConnection expired={()=>signedOut('Your session has expired. Please sign in again.')}/>{user.role==='admin'&&<AddonSettings expired={()=>signedOut('Your session has expired. Please sign in again.')}/>}</div></>;
}
function People({ user, signedOut, minimum }: { user: User; minimum: number; signedOut: (message: string) => void }) {
  const [users, setUsers] = useState<User[]>([]), [next, setNext] = useState<number | undefined>(), [loaded, setLoaded] = useState(false), [loading, setLoading] = useState(false), [error, setError] = useState(''), [notice, setNotice] = useState(''), [confirm, setConfirm] = useState<number | null>(null);
  const create = useAction(), change = useAction(); const read = useRef<AbortController | null>(null);
  async function load(after?: number) {
    read.current?.abort(); const controller = new AbortController(); read.current = controller; setLoading(true); setError('');
    try {
      const page = await api.users(after, controller.signal);
      if (!controller.signal.aborted) { setUsers(previous => after === undefined ? page.users : [...previous, ...page.users]); setNext(page.next_after_id); setLoaded(true); }
    } catch (e) {
      if (!controller.signal.aborted) {
        if (e instanceof ApiError && e.status === 401) signedOut('Your session has expired. Please sign in again.'); else setError(errorMessage(e));
      }
    } finally { if (!controller.signal.aborted) setLoading(false); }
  }
  useEffect(() => { void load(); return () => read.current?.abort(); }, []);
  function submit(e: FormEvent<HTMLFormElement>) {
    e.preventDefault(); const form = e.currentTarget;
    void create.run(() => { validatePassword(field(form, 'password'), minimum); return api.createUser(field(form, 'username'), field(form, 'display_name'), field(form, 'password'), field(form, 'role') as User['role']); }, () => { form.reset(); setNotice('Account created. Share its credentials privately with its owner.'); void load(); });
  }
  const busy = create.pending || change.pending;
  return <><div className="page-heading"><p className="eyebrow">ADMINISTRATION</p><h1>People in your district</h1><p>Create local accounts and manage who can sign in.</p></div><Notice>{notice}</Notice><div className="columns people"><section className="card"><div className="section-title"><h2>TED accounts</h2><button className="subtle" disabled={loading || busy} onClick={() => void load()}>Refresh</button></div><Notice error>{error}</Notice><Notice error>{change.error}</Notice>{!loaded && loading && <p role="status">Loading accounts…</p>}{loaded && users.length === 0 && <p>No accounts found.</p>}<ul className="users">{users.map(person => <li key={person.id}><div className="user-row"><div className="user-name"><strong>{person.display_name}{person.id === user.id && <small> (you)</small>}</strong><span>@{person.username} · {person.role === 'admin' ? 'Administrator' : 'Member'} · {person.disabled ? 'Disabled' : 'Active'}</span></div><button className="subtle" disabled={busy || loading} onClick={() => { setConfirm(person.id); change.setError(''); }}>{person.disabled ? 'Enable' : 'Disable'}</button></div>{confirm === person.id && <div className="confirmation"><p>{person.disabled ? `Allow ${person.username} to sign in again?` : `Disable ${person.username}? All their sessions will be revoked.`}</p>{person.id === user.id && <p>This will sign you out. TED must retain another active administrator.</p>}<div className="actions"><button disabled={busy} onClick={() => void change.run(() => api.setDisabled(person.id, !person.disabled), () => { setConfirm(null); if (person.id === user.id && !person.disabled) signedOut('Your account was disabled.'); else { setNotice('Account updated.'); void load(); } })}>Confirm {person.disabled ? 'enable' : 'disable'}</button><button className="subtle" disabled={busy} onClick={() => setConfirm(null)}>Cancel</button></div></div>}</li>)}</ul>{next !== undefined && <button disabled={loading || busy} onClick={() => void load(next)}>{loading ? 'Loading…' : 'Load more accounts'}</button>}{error && <button disabled={loading || busy} onClick={() => void load()}>Reload accounts</button>}</section><section className="card"><h2>Add a person</h2><p>Give someone their own space in TED.</p><form onSubmit={submit}><fieldset disabled={busy}><label>Username<input name="username" required minLength={3} maxLength={64} pattern="[A-Za-z0-9][A-Za-z0-9_.\-]{2,63}" autoComplete="off" autoCapitalize="none" spellCheck={false}/></label><p className="hint">3–64 letters, digits, dots, underscores or hyphens.</p><label>Display name<input name="display_name" required maxLength={100} autoComplete="off"/></label><Password name="password" label="Initial password" fresh/><p className="hint">{minimum === 0 ? "Leave blank for no password." : `Minimum ${minimum} characters.`} Maximum 1,024 UTF-8 bytes.</p><label>Role<select name="role" defaultValue="user"><option value="user">Member</option><option value="admin">Administrator</option></select></label><p className="hint">Administrators can create accounts and enable or disable access.</p><Notice error>{create.error}</Notice><button className="primary" type="submit">{create.pending ? 'Creating…' : 'Create account'}</button></fieldset></form></section></div></>;
}

function DisplayName({ user, updated }: { user: User; updated: (user: User) => void }) {
  const action = useAction();
  const [saved, setSaved] = useState(false);
  function submit(event: FormEvent<HTMLFormElement>) {
    event.preventDefault();
    const form = event.currentTarget;
    let changed: User;
    setSaved(false);
    void action.run(async () => { changed = await api.profile(field(form, 'display_name')); }, () => {
      updated(changed); setSaved(true);
    });
  }
  return <section className="card profile-name">
    <h2>Display name</h2>
    <p>How your name appears in TED. Your login username stays @{user.username}.</p>
    <form onSubmit={submit}><fieldset disabled={action.pending}>
      <label>Display name<input name="display_name" defaultValue={user.display_name} required maxLength={100} autoComplete="nickname"/></label>
      <Notice error>{action.error}</Notice>
      <Notice>{saved ? 'Display name saved.' : ''}</Notice>
      <button type="submit">{action.pending ? 'Saving…' : 'Save display name'}</button>
    </fieldset></form>
  </section>;
}
