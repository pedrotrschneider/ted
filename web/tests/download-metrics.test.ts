import test from 'node:test';
import assert from 'node:assert/strict';
import { downloadMetrics } from '../src/download-metrics.ts';
test('download rate uses Mbps and ETA rounds up with useful units', () => {
 assert.equal(downloadMetrics(100, 59), '100.0 Mbps · About less than a minute remaining');
 assert.match(downloadMetrics(12.35, 61), /Mbps · About 2 min remaining$/);
 assert.match(downloadMetrics(100, 3601), /About 1 h 1 min remaining$/);
 assert.match(downloadMetrics(null, null), /unavailable/);
});
