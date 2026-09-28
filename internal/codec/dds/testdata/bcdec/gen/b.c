#define BCDEC_STATIC
#define BCDEC_BC4BC5_PRECISE
#define BCDEC_IMPLEMENTATION
#include "../bcdec.h"
void b_bc4s(const void*s,void*d){bcdec_bc4(s,d,4,1);}
void b_bc5s(const void*s,void*d){bcdec_bc5(s,d,8,1);}
