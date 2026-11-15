package main

// The notices the answer loop sends when it ends a question itself.

// questionTimeoutNotice is what the asker hears when the question ran past
// agent.question_timeout.
const questionTimeoutNotice = "That took too long, so I stopped. Try a narrower question."

// panicNotice is what the asker hears when answering the question crashed.
const panicNotice = "Something went wrong answering that, so I stopped. Try asking another way."

// reRunNotice is what the asker hears when the question was started again
// after too many lost connections to the game.
const reRunNotice = "I kept losing touch with the game while answering that, so I stopped. Try asking again."
