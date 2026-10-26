import {copyFileSync,chmodSync} from 'node:fs';
copyFileSync(new URL('../../icon.png', import.meta.url), new URL('../public/icon.png', import.meta.url));
// The source image may arrive with owner-only permissions. Public build assets
// must remain readable by the unprivileged user in the runtime container.
chmodSync(new URL('../public/icon.png', import.meta.url), 0o644);
