import {copyFileSync} from 'node:fs';
copyFileSync(new URL('../../icon.png', import.meta.url), new URL('../public/icon.png', import.meta.url));
