import {test} from 'node:test';
import assert from 'node:assert/strict';
import {effortAngle, effortFraction, effortIndex, effortTone} from './effort-ui.mjs';
const options=[{id:'default',label:'Default'},{id:'low',label:'Low'},{id:'medium',label:'Medium'},{id:'high',label:'High'}];
test('needle spans ordered levels and default stays neutral',()=>{assert.equal(effortAngle(options,'low'),-120);assert.equal(effortAngle(options,'medium'),0);assert.equal(effortAngle(options,'high'),120);assert.equal(effortAngle(options,'default'),0);assert.equal(effortAngle(options,'default','high'),120)});
test('unknown selection resolves to Default, not the old slider index',()=>{assert.equal(effortIndex(options,'xhigh'),0);assert.equal(effortIndex(options,'medium'),2)});
test('automatic and off levels have distinct gauge semantics',()=>{const list=[{id:'default'},{id:'off'},{id:'low'},{id:'high'},{id:'auto'}];assert.equal(effortAngle(list,'off'),-120);assert.equal(effortAngle(list,'auto'),0);assert.equal(effortAngle(list,'high'),120)});
test('arc fill and tone follow the level',()=>{const l=[{id:'default'},{id:'low'},{id:'medium'},{id:'high'},{id:'xhigh'}];assert.equal(effortFraction(l,'low'),0);assert.equal(effortFraction(l,'xhigh'),1);assert.equal(effortTone(l,'low'),'39 177 153');assert.equal(effortTone(l,'xhigh'),'239 68 68');assert.equal(effortTone(l,'default','high'),'148 163 184');assert.equal(effortFraction(l,'default'),null)});
