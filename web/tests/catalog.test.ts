import test from 'node:test';
import assert from 'node:assert/strict';
import { appendMovies, catalogApi } from '../src/catalog-api.ts';
import { ApiError } from '../src/api.ts';
const movie = { id: 'tt1345836', type: 'movie', name: 'The Dark Knight Rises', year: '2012', description: '', poster: '', genres: [] };
const mapping = { id: '11111111-1111-4111-8111-111111111111', movie_id: '22222222-2222-4222-8222-222222222222', movie, rd_account_id: '1234', torrent_id: 'T1', hash: 'a'.repeat(40), file_id: 3, path: '/movie.mkv', bytes: 25275015542, source_name: 'Release', status: 'draft', created_at: '2026-10-08T12:00:00Z' };
test('live Metahub posters are accepted while lookalikes and insecure URLs are rejected', async () => {
  const original = globalThis.fetch;
  try {
    const poster = 'https://live.metahub.space/poster/small/tt1345836/img';
    globalThis.fetch = async () => Response.json({ ...movie, poster });
    assert.equal((await catalogApi.movie(movie.id)).poster, poster);
    for (const invalid of ['https://live.metahub.space.evil.invalid/img', 'http://live.metahub.space/img', 'https://user@live.metahub.space/img']) {
      globalThis.fetch = async () => Response.json({ ...movie, poster: invalid });
      await assert.rejects(catalogApi.movie(movie.id), /incomplete/);
    }
  } finally { globalThis.fetch = original; }
});
test('Cinemeta pages distinguish empty results, malformed results and overlapping pagination', async () => {
  const original = globalThis.fetch;
  try {
    globalThis.fetch = async () => Response.json({ movies: [movie], skip: 0, next_skip: 50 });
    const first = await catalogApi.browse();
    assert.equal(first.movies[0].id, movie.id);
    assert.throws(() => appendMovies(first, { ...first, skip: 50, next_skip: 100 }), /changed/);
    assert.equal(first.movies.length, 1);
    globalThis.fetch = async () => Response.json({ movies: [], skip: 50 });
    assert.equal(appendMovies(first, await catalogApi.browse('', 50)).next_skip, undefined);
    globalThis.fetch = async () => Response.json({ movies: null, skip: 0 });
    await assert.rejects(catalogApi.browse(), /incomplete/);
    globalThis.fetch = async () => Response.json({ ...movie, poster: 'https://untrusted.invalid/poster' });
    await assert.rejects(catalogApi.movie(movie.id), /incomplete/);
  } finally { globalThis.fetch = original; }
});
test('mapping save sends explicit observed file identity and validates the confirmation', async () => {
  const original = globalThis.fetch;
  const file = { id: 3, path: mapping.path, bytes: mapping.bytes, selected: true, video: true, suggested_exclusion: false };
  const source = { id: 'T1', hash: mapping.hash, name: 'Release', files: [file], bytes: file.bytes, status: 'downloaded', progress: 100, added: mapping.created_at };
  try {
    globalThis.fetch = async (url, options) => {
      assert.equal(url, `/api/admin/movies/${movie.id}/mapping`); assert.equal(options?.method, 'PUT');
      assert.deepEqual(JSON.parse(String(options?.body)), { torrent_id: 'T1', hash: mapping.hash, file_id: 3, path: file.path, bytes: file.bytes });
      return Response.json({publication_mode:'native',id:'operation',phase:'queued',error:'',total:1,validated:0,batch:1,batches:1,completed_batches:0,committed:0,retryable:false});
    };
    assert.equal((await catalogApi.save(movie.id, source, file)).phase, 'queued');
    globalThis.fetch = async () => Response.json({ ...mapping, path: '/wrong.mkv' });
    await assert.rejects(catalogApi.save(movie.id, source, file), /outcome is unknown/);
    globalThis.fetch = async () => Response.json({ code: 'mapping_conflict', error: 'untrusted text' }, { status: 409 });
    await assert.rejects(catalogApi.save(movie.id, source, file), (e: unknown) => e instanceof ApiError && e.status === 409 && e.message.includes('mapping operation is pending') && !e.message.includes('untrusted'));
  } finally { globalThis.fetch = original; }
});
test('saved mapping read distinguishes absence from failures and wrong movie identity', async () => {
  const original = globalThis.fetch;
  try {
    globalThis.fetch = async () => Response.json({ mapping: null });
    assert.equal(await catalogApi.mapping(movie.id), null);
    globalThis.fetch = async () => Response.json({ mapping });
    assert.equal((await catalogApi.mapping(movie.id))?.id, mapping.id);
    await assert.rejects(catalogApi.mapping('tt0111161'), /incomplete/);
    globalThis.fetch = async () => Response.json({}, { status: 503 });
    await assert.rejects(catalogApi.mapping(movie.id), /unavailable/);
  } finally { globalThis.fetch = original; }
});
