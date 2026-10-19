export const ratingNames=['Absolute trash','Very Bad','Bad','Average','Good','Very Good','Absolute cinema'];
export const ratingColors=['#851f28','#bd4248','#df777b','#656d73','#79bd8d','#359b65','#145b3a'];
export function validRating(value:unknown){return value===null||(Number.isInteger(value)&&Number(value)>=1&&Number(value)<=7)}
