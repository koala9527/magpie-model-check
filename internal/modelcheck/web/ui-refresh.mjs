// Polling must not replace an unchanged report: native details, selection and
// keyboard focus belong to the user, not to the two-second refresh timer.
export function createRenderGuard(){
  let previous;
  return value=>{
    const next=JSON.stringify(value);
    if(next===previous)return false;
    previous=next;
    return true;
  };
}
