package redisstore_test

// minimalACL is the ACL user the README documents (RS-09). The integration
// suite creates exactly this user and proves it is enough; TestREADME proves
// the README still says it.
const minimalACL = "ACL SETUSER cistern on >s3cret-for-tests resetkeys ~cistern:* resetchannels -@all +get +set +del"
