import {createRoot} from 'react-dom/client';
import {MediaGrid} from '../src/MediaPresentation';
import {TitleStatusBadge} from '../src/TitleStatus';
import type {TitleStatus} from '../src/title-status-api';
import type {Movie} from '../src/catalog-api';
import '../src/style.css';

const titles:Movie[]=Array.from({length:7},(_,i)=>({id:`tt${4000000+i}`,type:'movie',name:`Rating ${i+1}`,year:'2026',description:'',poster:'',genres:[]}));
function status(id:string):TitleStatus{
 const rating=titles.findIndex(t=>t.id===id)+1,watch={seen:true,watched:1,available:1,progress:null};
 return {id,watch,watchlist:true,user_rating:rating,publication_mode:'native',state:'added',phase:'complete',added:1,total:1,validated:1,
  joint_watches:[{account_id:8,name:'Joint',color:'#59C6F2',rating,watch}],
  user_watches:[{account_id:9,name:'Pedro',rating,watch:rating%2===0?{...watch,watched:0,progress:35}:watch}]};
}
window.fetch=async input=>{
 const url=new URL(String(input),location.origin),ids=(url.searchParams.get('ids')??'').split(',');
 if(url.pathname==='/api/title-statuses')return Response.json({statuses:ids.map(status)});
 if(url.pathname==='/api/catalog-ratings')return Response.json({ratings:ids.map(id=>({id,rating:7.2,pending:false}))});
 throw Error(`Unexpected fixture request: ${url.pathname}`);
};
createRoot(document.getElementById('root')!).render(<main style={{padding:20}}>
 <MediaGrid titles={titles}/>
 <section className="movie-detail"><div className="title-poster"><div className="poster poster-empty">TED</div><TitleStatusBadge status={status(titles[3].id)} kind="movie"/></div></section>
</main>);
