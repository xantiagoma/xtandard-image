#define BCDEC_STATIC
#define BCDEC_IMPLEMENTATION
#include "../bcdec.h"
void a_bc1(const void*s,void*d){bcdec_bc1(s,d,16);}
void a_bc2(const void*s,void*d){bcdec_bc2(s,d,16);}
void a_bc3(const void*s,void*d){bcdec_bc3(s,d,16);}
void a_bc4(const void*s,void*d){bcdec_bc4(s,d,4);}
void a_bc5(const void*s,void*d){bcdec_bc5(s,d,8);}
void a_bc6(const void*s,void*d,int sg){bcdec_bc6h_half(s,d,12,sg);}
void a_bc7(const void*s,void*d){bcdec_bc7(s,d,16);}
