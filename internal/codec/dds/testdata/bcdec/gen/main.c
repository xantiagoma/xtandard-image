/* Generator of the BCn reference vectors in ../ (internal/codec/dds
 * bc_test.go). bcdec.h is not vendored: fetch v0.985 of
 * https://github.com/iOrange/bcdec (MIT) next to this directory's parent,
 * then, from testdata/bcdec:
 *
 *   cc -O1 -I. -o /tmp/bcgen gen/main.c gen/a.c gen/b.c && /tmp/bcgen
 *
 * a.c and b.c include "../bcdec.h" relative to gen/. The generator writes
 * <format>.bin: 288 records of a pseudo-random block (xorshift64, fixed
 * seed; BC7 blocks cycle through modes 0-7 and the reserved mode, BC6H
 * blocks through every mode code) followed by bcdec's decoding. */
#include <stdio.h>
#include <stdint.h>
#include <string.h>
void a_bc1(const void*,void*);void a_bc2(const void*,void*);void a_bc3(const void*,void*);
void a_bc4(const void*,void*);void a_bc5(const void*,void*);void a_bc6(const void*,void*,int);void a_bc7(const void*,void*);
void b_bc4s(const void*,void*);void b_bc5s(const void*,void*);
static uint64_t st=0x9E3779B97F4A7C15ull;
static uint64_t rnd(void){st^=st<<13;st^=st>>7;st^=st<<17;return st;}
static void fill(uint8_t*b,int n){for(int i=0;i<n;i++)b[i]=(uint8_t)(rnd()>>32);}
static const int bc6codes[18]={0,1,2,6,10,14,18,22,26,30,3,7,11,15,19,23,27,31};
int main(void){
  int N=288; uint8_t in[16], out[256];
  const char* names[]={"bc1","bc2","bc3","bc4u","bc4s","bc5u","bc5s","bc6hu","bc6hs","bc7"};
  int bs[]={8,16,16,8,8,16,16,16,16,16};
  int os[]={64,64,64,16,16,32,32,96,96,64};
  for(int f=0;f<10;f++){
    char p[64]; snprintf(p,64,"%s.bin",names[f]); FILE*fp=fopen(p,"wb");
    for(int k=0;k<N;k++){
      fill(in,16);
      if(f==7||f==8){int c=bc6codes[k%18]; if(c<2){in[0]=(in[0]&~3)|c;} else {in[0]=(in[0]&~31)|c;}}
      if(f==9){int m=k%9; if(m<8){in[0]=(in[0]&~((1<<(m+1))-1))|(1<<m);} else in[0]=0;}
      if((f<=2) && (k&1)){ int o=(f==0)?0:8; uint8_t t0=in[o],t1=in[o+1]; in[o]=in[o+2];in[o+1]=in[o+3];in[o+2]=t0;in[o+3]=t1;}
      memset(out,0,sizeof out);
      switch(f){case 0:a_bc1(in,out);break;case 1:a_bc2(in,out);break;case 2:a_bc3(in,out);break;
        case 3:a_bc4(in,out);break;case 4:b_bc4s(in,out);break;case 5:a_bc5(in,out);break;case 6:b_bc5s(in,out);break;
        case 7:a_bc6(in,out,0);break;case 8:a_bc6(in,out,1);break;case 9:a_bc7(in,out);break;}
      fwrite(in,1,bs[f],fp); fwrite(out,1,os[f],fp);
    }
    fclose(fp);
  }
  return 0;
}
