// Heights include the badge border. Reserve a whole final row for overflow.
export function peopleWatchLayout(count:number,available:number,normal:number,minimum:number,gap:number){
 if(count===0)return {visible:0,hidden:0,height:normal,offset:0};
 const fitted=(available-gap*(count-1))/count;
 if(fitted>=minimum)return {visible:count,hidden:0,height:Math.min(normal,fitted),offset:0};
 const slots=Math.max(1,Math.floor((available+gap)/(minimum+gap)));
 const visible=Math.min(count-1,slots-1),rows=visible+1;
 const height=Math.max(minimum,Math.min(normal,(available-gap*(rows-1))/rows));
 return {visible,hidden:count-visible,height,offset:Math.min(0,available-height)};
}
