const {test} = require('node:test');
const assert = require('node:assert/strict');
const {parseRinexHeader, preflightAdvice, escape, qualityText} = require('./workflow.js');
const line = (body,label) => body.padEnd(60) + label;
const version = '     3.04           O                   M';
test('RINEX header reports dates, antenna and two phase bands per system', () => {
 const h = parseRinexHeader([
  line(version,'RINEX VERSION / TYPE'),
  line('serial'.padEnd(20)+'TRM57971.00     NONE','ANT # / TYPE'),
  line('  2020     1     2     3     4    5.0000000     GPS','TIME OF FIRST OBS'),
  line('  2020     1     2     4     4    5.0000000     GPS','TIME OF LAST OBS'),
  line('G    4 C1C L1C C2W L2W','SYS / # / OBS TYPES'),line('','END OF HEADER')].join('\n'));
 assert.equal(h.valid,true); assert.equal(h.complete,true); assert.equal(h.last.seconds-h.first.seconds,3600);
 assert.deepEqual(h.phaseBands.G,['1','2']);assert.equal(h.antenna,'TRM57971.00     NONE'); assert.equal(preflightAdvice(h,'ppp-static'),'');
});
test('bands from separate systems do not imply dual frequency phase observations', () => {
 const h=parseRinexHeader([line(version,'RINEX VERSION / TYPE'),line('G    2 C1C L1C','SYS / # / OBS TYPES'),line('E    2 C5Q L5Q','SYS / # / OBS TYPES'),line('','END OF HEADER')].join('\n'));
 assert.match(preflightAdvice(h,'ppp-static'),/двух частотах/);
});
test('navigation and arbitrary files are rejected, incomplete headers are not overdiagnosed', () => {
 assert.equal(parseRinexHeader('hello').valid,false);
 assert.equal(parseRinexHeader(line('     3.04           N','RINEX VERSION / TYPE')).valid,false);
 assert.equal(preflightAdvice(parseRinexHeader(line(version,'RINEX VERSION / TYPE')),'ppp-static'),'');
});
test('untrusted header strings are escaped and zero FIX is visible', () => {
 assert.equal(escape('<img onerror="x">'), '&lt;img onerror=&quot;x&quot;&gt;');
 assert.match(qualityText(6,0),/0.0%/);
});
