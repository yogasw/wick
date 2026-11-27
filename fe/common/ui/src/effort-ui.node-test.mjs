import {test} from 'node:test';
import assert from 'node:assert/strict';
import {effortAngle, effortIndex} from './effort-ui.mjs';
const options=[{id:'default',label:'Default'},{id:'low',label:'Low'},{id:'medium',label:'Medium'},{id:'high',label:'High'}];
test('needle spans ordered levels and default stays neutral',()=>{assert.equal(effortAngle(options,'low'),-70);assert.equal(effortAngle(options,'medium'),0);assert.equal(effortAngle(options,'high'),70);assert.equal(effortAngle(options,'default'),0);assert.equal(effortAngle(options,'default','high'),70)});
test('unknown selection resolves to Default, not the old slider index',()=>{assert.equal(effortIndex(options,'xhigh'),0);assert.equal(effortIndex(options,'medium'),2)});
test('automatic and off levels have distinct gauge semantics',()=>{const list=[{id:'default'},{id:'off'},{id:'low'},{id:'high'},{id:'auto'}];assert.equal(effortAngle(list,'off'),-70);assert.equal(effortAngle(list,'auto'),0);assert.equal(effortAngle(list,'high'),70)});
