import test from 'node:test';
import assert from 'node:assert/strict';
import { bindFormAction } from './ui-forms.mjs';

class Button extends EventTarget { disabled=false; }
class Form extends EventTarget {
  buttons=[new Button()];
  valid=true;
  checks=0;
  querySelectorAll(){return this.buttons;}
  reportValidity(){this.checks++;return this.valid;}
}
const turn=()=>new Promise(resolve=>setImmediate(resolve));

test('a sandbox button click saves without relying on a native submit event',async()=>{
  const form=new Form(),saved=[];
  bindFormAction(form,async value=>saved.push(value));
  const click=new Event('click',{cancelable:true});
  form.buttons[0].dispatchEvent(click);
  await turn();
  assert.equal(click.defaultPrevented,true);
  assert.deepEqual(saved,[form]);
  assert.equal(form.checks,1);
  assert.equal(form.buttons[0].disabled,false);
});

test('invalid data and repeated clicks do not produce requests',async()=>{
  const form=new Form();let saved=0,finish;
  bindFormAction(form,()=>{saved++;return new Promise(resolve=>finish=resolve);});
  form.valid=false;form.buttons[0].dispatchEvent(new Event('click'));
  assert.equal(saved,0);
  form.valid=true;form.buttons[0].dispatchEvent(new Event('click'));
  form.buttons[0].dispatchEvent(new Event('click'));
  form.dispatchEvent(new Event('submit'));
  assert.equal(saved,1);
  assert.equal(form.buttons[0].disabled,true);
  finish();await turn();
  assert.equal(form.buttons[0].disabled,false);
});

test('Enter saves a text input but keeps textarea newlines and IME composition',async()=>{
  const form=new Form();let saved=0;
  bindFormAction(form,async()=>saved++);
  const key=(tag,key='Enter',isComposing=false)=>{
    const event=new Event('keydown',{cancelable:true});
    Object.defineProperties(event,{key:{value:key},isComposing:{value:isComposing},target:{value:{tagName:tag,type:'text'}}});
    return event;
  };
  form.dispatchEvent(key('TEXTAREA'));form.dispatchEvent(key('INPUT','Enter',true));
  assert.equal(saved,0);
  const enter=key('INPUT');form.dispatchEvent(enter);await turn();
  assert.equal(saved,1);assert.equal(enter.defaultPrevented,true);
});
