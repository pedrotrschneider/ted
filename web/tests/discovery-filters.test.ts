import test from 'node:test';import assert from 'node:assert/strict';
import {discoveryFilters,browseHeading} from '../src/discovery-filters.ts';
test('discovery changes between genre, release year and unrestricted text search',()=>{
 const selection={catalog:'popular' as const,genre:'Action',year:2026,saved:false,watched:false,search:''};
 assert.deepEqual(discoveryFilters(selection),{catalog:'popular',genre:'Action'});
 assert.deepEqual(discoveryFilters({...selection,catalog:'new'}),{catalog:'new',year:2026});
 assert.deepEqual(discoveryFilters({...selection,catalog:'featured',search:'Batman'}),{});
});

const headingSelection={catalog:'popular' as const,genre:'Horror',year:2025,saved:false,watched:false,search:''};
test('discovery headings include the selected genre or release year for both media types',()=>{
 assert.equal(browseHeading('movie',headingSelection),'Popular Horror Movies');
 assert.equal(browseHeading('movie',{...headingSelection,catalog:'new'}),'2025 Movies');
 assert.equal(browseHeading('series',{...headingSelection,catalog:'featured'}),'Featured Horror Series');
 assert.equal(browseHeading('series',{...headingSelection,catalog:'new'}),'2025 Series');
 assert.equal(browseHeading('movie',{...headingSelection,genre:''}),'Popular Movies');
});
test('personal headings describe both toggles and genre without inactive catalog filters',()=>{
 assert.equal(browseHeading('movie',{...headingSelection,saved:true,watched:true,catalog:'new'}),'Mapped and Watched Horror Movies');
 assert.equal(browseHeading('series',{...headingSelection,watched:true,genre:''}),'Watched Series');
});
test('search headings include only filters actually applied to the query',()=>{
 assert.equal(browseHeading('movie',{...headingSelection,catalog:'new',search:'Batman'}),'Movies matching “Batman”');
 assert.equal(browseHeading('movie',{...headingSelection,saved:true,search:'Batman'}),'Mapped Horror Movies matching “Batman”');
});
