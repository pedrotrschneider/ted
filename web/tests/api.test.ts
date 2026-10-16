import { test } from 'node:test';
import assert from 'node:assert/strict';
import { api, ApiError } from '../src/api.ts';
const account = { id: 1, username: 'admin', display_name: 'Administrator', role: 'admin', disabled: false };
test('login confirms that the browser retained a session and sends same-origin cookies', async t => {
 const paths: string[] = [];
 t.mock.method(globalThis, 'fetch', async (path: string, init: RequestInit) => { paths.push(path); assert.equal(init.credentials, 'same-origin'); assert.equal(init.cache, 'no-store'); return Response.json(account); });
 assert.deepEqual(await api.login('admin', 'not-a-real-password'), account);
 assert.deepEqual(paths, ['/api/auth/login', '/api/auth/me']);
});
test('a rejected cookie does not report a successful sign-in', async t => {
 t.mock.method(globalThis, 'fetch', async (path: string) => path.endsWith('/login') ? Response.json(account) : Response.json({}, { status: 401 }));
 await assert.rejects(api.login('admin', 'not-a-real-password'), /could not retain/);
});
test('an unavailable or partial list never becomes a successful empty list', async t => {
 const mock = t.mock.method(globalThis, 'fetch', async () => Response.json({}, { status: 503 }));
 await assert.rejects(api.users(), (e: unknown) => e instanceof ApiError && e.status === 503);
 mock.mock.mockImplementation(async () => Response.json({ users: [account, { id: 2 }] }));
 await assert.rejects(api.users(), /invalid account/);
 mock.mock.mockImplementation(async () => Response.json({ users: [account], next_after_id: 9 }));
 await assert.rejects(api.users(), /invalid account page/);
});
test('pagination validates ordering and supports a complete empty result', async t => {
 const mock = t.mock.method(globalThis, 'fetch', async () => Response.json({ users: [] }));
 assert.deepEqual(await api.users(), { users: [], next_after_id: undefined });
 mock.mock.mockImplementation(async () => Response.json({ users: [account] }));
 await assert.rejects(api.users(1), /invalid account page/);
});
test('writes are never retried after a lost confirmation', async t => {
 const mock = t.mock.method(globalThis, 'fetch', async () => { throw new TypeError('network'); });
 await assert.rejects(api.setDisabled(2, true), /may have completed/);
 assert.equal(mock.mock.callCount(), 1);
});
test('server failure and malformed acknowledgments leave a mutation unconfirmed', async t => {
 const mock = t.mock.method(globalThis, 'fetch', async () => Response.json({}, { status: 503 }));
 await assert.rejects(api.password('old-password', 'new-password'), /may have completed/);
 mock.mock.mockImplementation(async () => Response.json({}));
 await assert.rejects(api.logout(), /invalid confirmation/);
 assert.equal(mock.mock.callCount(), 2);
});
test('obsolete reads propagate cancellation distinctly from failures', async t => {
 t.mock.method(globalThis, 'fetch', async (_: string, init: RequestInit) => { init.signal!.throwIfAborted(); throw new Error('expected abort'); });
 const controller = new AbortController(); controller.abort();
 await assert.rejects(api.users(undefined, controller.signal), { name: 'AbortError' });
});

test('password policy validates optional, Unicode and maximum-length input', async t => {
 const { validatePassword } = await import('../src/api.ts');
 validatePassword('', 0); validatePassword('x', 1); validatePassword('猫é', 2);
 assert.throws(() => validatePassword('猫', 2), ApiError);
 assert.throws(() => validatePassword('猫'.repeat(342), 0), ApiError);
 t.mock.method(globalThis, 'fetch', async () => Response.json({min_password_length: 0}));
 assert.equal(await api.policy(), 0);
});
test('profile updates send only a display name and require a valid account response', async t => {
 t.mock.method(globalThis, 'fetch', async (path: string, init: RequestInit) => {
   assert.equal(path, '/api/auth/profile');assert.equal(init.method, 'PUT');
   assert.deepEqual(JSON.parse(String(init.body)), {display_name:'María 猫'});
   return Response.json({...account, display_name:'María 猫'});
 });
 assert.equal((await api.profile('María 猫')).display_name, 'María 猫');
});
test('profile pictures upload binary data and require a validated account image version', async t => {
 const image = new Blob([new Uint8Array([137, 80, 78, 71])], {type:'image/png'});
 const changed = {...account, avatar_version:'a'.repeat(64)};
 const mock = t.mock.method(globalThis, 'fetch', async (path: string, init: RequestInit) => {
  assert.equal(path, '/api/account/avatar'); assert.equal(init.method, 'PUT');
  assert.equal(init.body, image); assert.equal(new Headers(init.headers).get('Content-Type'), 'image/png');
  return Response.json(changed);
 });
 assert.deepEqual(await api.uploadAvatar(image), changed);
 mock.mock.mockImplementation(async () => Response.json(account));
 await assert.rejects(api.uploadAvatar(image), /did not confirm the profile picture/);
 mock.mock.mockImplementation(async () => Response.json({...changed, avatar_version:'not-an-image-version'}));
 await assert.rejects(api.uploadAvatar(image), /invalid profile picture/);
 mock.mock.mockImplementation(async () => Response.json({code:'avatar_invalid'}, {status:400}));
 await assert.rejects(api.uploadAvatar(image), /JPEG, PNG, WebP or GIF/);
});

test('ordinary browser sign-out and explicit all-device sign-out use separate actions', async t => {
 const paths: string[] = [];
 t.mock.method(globalThis, 'fetch', async (path: string, init: RequestInit) => {
  paths.push(path); assert.equal(init.method, 'POST');
  return Response.json({status: 'signed out'});
 });
 await api.logout(); await api.logoutAll();
 assert.deepEqual(paths, ['/api/auth/logout-browser', '/api/auth/logout']);
});

test('setup status fails closed on unavailable or malformed responses', async t => {
 const mock = t.mock.method(globalThis, 'fetch', async () => Response.json({setup_required: true}));
 assert.equal(await api.setupRequired(), true);
 mock.mock.mockImplementation(async () => Response.json({setup_required: false}));
 assert.equal(await api.setupRequired(), false);
 mock.mock.mockImplementation(async () => Response.json({}));
 await assert.rejects(api.setupRequired(), /invalid setup/);
 mock.mock.mockImplementation(async () => Response.json({}, {status:503}));
 await assert.rejects(api.setupRequired(), ApiError);
});
test('setup requires confirmation and does not retry an uncertain creation', async t => {
 const mock = t.mock.method(globalThis, 'fetch', async () => Response.json({status:'administrator created'}, {status:201}));
 await api.setup('owner', 'password');
 mock.mock.mockImplementation(async () => Response.json({}));
 await assert.rejects(api.setup('owner', 'password'), /invalid setup confirmation/);
 mock.mock.mockImplementation(async () => {throw new TypeError('network');});
 await assert.rejects(api.setup('owner', 'password'), /may have completed/);
 assert.equal(mock.mock.callCount(), 3);
});
