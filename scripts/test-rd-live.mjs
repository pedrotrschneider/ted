// Explicit opt-in: read the owner's fixtures and save only disposable TED mappings.
import fs from 'node:fs';
import {parseEnv} from 'node:util';
import {spawnSync} from 'node:child_process';
const key=parseEnv(fs.readFileSync('.env','utf8')).TED_REAL_DEBRID_API_KEY;
if(!key?.trim())throw new Error('TED_REAL_DEBRID_API_KEY is missing from .env');
if(process.argv.slice(2).some(arg=>arg!=='--compose'))throw new Error('usage: test-rd-live.mjs [--compose]');
const result=spawnSync('bash',process.argv.includes('--compose')?['scripts/compose-smoke.sh']:['scripts/test-database.sh','behavior'],{stdio:'inherit',env:{...process.env,TED_REAL_DEBRID_API_KEY:key.trim(),TED_RD_LIVE_TEST:'1',TED_MOVIE_LIVE_TEST:'1'}});
if(result.error)throw new Error('Unable to start live verification');
process.exitCode=result.status??1;
